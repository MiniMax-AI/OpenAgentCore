import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import test from "node:test";

// Keep the real pinned SDK and bridge; only the native subprocess is controlled.
// A delayed control reply exercises the SDK's deadline without a model request.
const native = `
import { createInterface } from "node:readline";
const lines=createInterface({input:process.stdin});
let timer;
const reply=(id,response)=>process.stdout.write(JSON.stringify({type:"control_response",response:{subtype:"success",request_id:id,response}})+"\\n");
lines.on("line",line=>{
 const message=JSON.parse(line);
 if(message.type==="control_request" && message.request.subtype==="initialize") {
  const started=performance.now();
  timer=setTimeout(()=>{
   process.send({kind:"initialized",elapsed:performance.now()-started});
   reply(message.request_id,{commands:[],models:[],account:{},hooks_applied:true});
  },Number(process.argv[1]));
 } else if(message.type==="control_request" && message.request.subtype==="mcp_status") {
  process.send({kind:"required_status"});
  reply(message.request_id,{mcpServers:[{name:"fixture",status:"connected",tools:[{name:"echo"}]}]});
 } else if(message.type==="user") process.send({kind:"input"});
 else throw new Error("Unexpected native control");
});
lines.on("close",()=>{clearTimeout(timer);process.disconnect();});
`;

const childProcess = `
import { spawn as nativeSpawn } from "node:child_process";
export function spawn(command,args,options) {
 const child=nativeSpawn(process.execPath,["--input-type=module","-e",${JSON.stringify(native)},process.argv[1]],{...options,stdio:[...options.stdio,"ipc"]});
 process.send({kind:"native_spawned",pid:child.pid});
 child.on("message",message=>process.send(message));
 child.once("close",()=>process.send({kind:"native_closed"}));
 return child;
}
`;
const bridge = `
import { registerHooks } from "node:module";
import { startup } from ${JSON.stringify(new URL("../node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs", import.meta.url).href)};
globalThis.actualStartup=args=>{process.send({kind:"budget",remaining:args.initializeTimeoutMs,now:Date.now()});return startup(args)};
registerHooks({resolve(specifier,context,next){
 if(specifier==="@anthropic-ai/claude-agent-sdk" && context.parentURL===${JSON.stringify(new URL("../dist/adapter.js", import.meta.url).href)})
  return {url:"data:text/javascript,"+encodeURIComponent("export { getSessionInfo,query } from " + ${JSON.stringify(JSON.stringify(new URL("../node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs", import.meta.url).href))} + ";export const startup=globalThis.actualStartup;"),shortCircuit:true};
 if(specifier==="node:child_process" && context.parentURL===${JSON.stringify(new URL("../dist/native.js", import.meta.url).href)})
  return {url:"data:text/javascript,"+encodeURIComponent(${JSON.stringify(childProcess)}),shortCircuit:true};
 return next(specifier,context);
}});
await import(${JSON.stringify(new URL("../dist/main.js", import.meta.url).href)});
process.disconnect();
`;

test("the SDK accepts delayed initialization on both adapter startup paths", {timeout:100000,concurrency:true}, async t => {
 await Promise.all(["executor", "required-mcp", "short", "expired", "cancel"].map(mode => t.test(mode, async t => {
  const parent=join(homedir(),".oac","tests");
  mkdirSync(parent,{recursive:true});
  const root=mkdtempSync(join(parent,"claude-startup-budget-"));
  const child=spawn(process.execPath,["--input-type=module","-e",bridge,mode==="required-mcp"?"16000":"61000"],{
   cwd:root,env:{PATH:process.env.PATH,HOME:root,CLAUDE_CONFIG_DIR:root},stdio:["pipe","pipe","pipe","ipc"],
  });
  const events=[],observations=[];
  const deadline=Date.now()+(mode==="expired"?-1:mode==="short"?2000:90000);
  let stderr="",nativePID;
  child.stderr.on("data",value=>{stderr+=value});
  const closed=new Promise(resolve=>child.once("close",(code,signal)=>resolve({code,signal})));
  t.after(async()=>{
   if(nativePID)try{process.kill(nativePID,"SIGKILL")}catch{}
   child.kill("SIGKILL");await closed;rmSync(root,{recursive:true,force:true});
  });
  const ready=new Promise((resolve,reject)=>{
   const notify=()=>{
    if(mode==="executor"?events.some(event=>event.type==="executor_ready") && observations.some(event=>event.kind==="initialized"):observations.some(event=>event.kind==="input"))resolve();
   };
   createInterface({input:child.stdout}).on("line",line=>{
    const event=JSON.parse(line);events.push(event);
    if(event.type==="error") { if(["short","expired","cancel"].includes(mode))resolve();else reject(new Error("Preparation failed: "+event.code)); }
    notify();
   });
   child.on("message",message=>{
    observations.push(message);
    if(message.kind==="native_spawned"){nativePID=message.pid;if(mode==="cancel")child.kill("SIGTERM");}
    if(message.kind==="native_closed")nativePID=undefined;
    notify();
   });
   child.once("close",()=>mode==="cancel"?resolve():reject(new Error("Bridge closed before readiness: "+stderr)));
  });
  child.stdin.write(JSON.stringify({type:mode==="required-mcp"?"start":"executor_prepare",cwd:root,model:"fixture",system_prompt:"",
   ...(mode!=="required-mcp"?{preparation_deadline:deadline}:{}),
   ...(mode==="required-mcp"?{input:[{content:[{type:"input_text",text:"held until initialization"}]}],
    mcp_http_servers:[{server_label:"fixture",server_url:"https://example.invalid/mcp",allowed_tools:["echo"],required:true}]}:{})})+"\n");
  await ready;
  if (["short","expired","cancel"].includes(mode)) {
   assert.equal(events.some(event=>event.type==="executor_ready"),false);
   assert.equal(observations.some(event=>event.kind==="input"),false);
   assert.equal(observations.some(event=>event.kind==="initialized"),false);
   if(mode==="cancel")assert.deepEqual(events,[]);else assert.equal(events.at(-1).code,"execution_failed");
  } else assert.ok(observations.find(event=>event.kind==="initialized").elapsed >= (mode==="executor"?60000:15000));
  const budget=observations.find(event=>event.kind==="budget");
  if(mode==="expired")assert.equal(budget,undefined);
  else if(mode!=="required-mcp") {assert.ok(budget.remaining>0);assert.ok(Math.abs(budget.now+budget.remaining-deadline)<20);}

  if (!["short","expired","cancel"].includes(mode)) assert.deepEqual(observations.filter(event=>event.kind!=="budget").map(event=>event.kind),mode==="executor"?["native_spawned","initialized"]:["native_spawned","initialized","required_status","input"]);
  if(mode==="executor")assert.deepEqual(events,[{type:"executor_ready",protocol:3}]);
  child.stdin.end();
  assert.deepEqual(await closed,{code:0,signal:null},stderr);
  assert.equal(observations.filter(event=>event.kind==="native_closed").length,mode==="expired"?0:1);
 })));
});
