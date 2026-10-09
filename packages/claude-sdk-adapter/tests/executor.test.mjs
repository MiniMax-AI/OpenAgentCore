import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const fixture = `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
const sdk = "export async function startup(args){return globalThis.startupFixture(args)} export function query(){throw new Error('one warm query required')} export async function getSessionInfo(){return {sessionId:'native'}}";
registerHooks({resolve(specifier,context,next){if(specifier==='@anthropic-ai/claude-agent-sdk') return {url:'data:text/javascript,'+encodeURIComponent(sdk),shortCircuit:true};return next(specifier,context)}});
globalThis.startupFixture=async({options})=>{
 const child=options.spawnClaudeCodeProcess({command:process.execPath,args:['-e',"process.stdin.resume();process.stdin.on('end',()=>process.exit(0))"],env:options.env,signal:options.abortController.signal});
 process.send({type:'native',pid:child.pid});
 const exited=new Promise(resolve=>child.once('close',()=>{process.send({type:'native_closed'});resolve()}));
 let client;
 if(options.mcpServers.functions){
  client=new Client({name:'fixture',version:'1'});
  const [a,b]=InMemoryTransport.createLinkedPair();
  await Promise.all([client.connect(a),options.mcpServers.functions.instance.connect(b)]);
 }
 let queried=false,interrupt,number=0;
 const mcp=process.argv[1].startsWith('mcp-');
 const statuses=['target','healthy'].map(name=>({name,status:'connected',tools:[{name:'hold'}]}));
 const close=()=>{child.stdin.end();interrupt?.()};
 options.abortController.signal.addEventListener('abort',close);
 return {close,query(prompt){assert.equal(queried,false);queried=true;process.send({type:'query'});return {
  close,async initializationResult(){if(process.argv[1]==="late-ready"){const expired=Date.now()+120000;Date.now=()=>expired;}return process.argv[1]==="no-hook-report" ? {} : process.argv[1]==="false-hook-report" ? {hooks_applied:false} : {hooks_applied:true}},
  async interrupt(){process.send({type:'interrupt'});interrupt?.();if(process.argv[1]==='rejected')throw new Error('interrupt failed');return ['unknown','pending-function-unknown'].includes(process.argv[1]) ? undefined : {still_queued:process.argv[1]==='queued' ? ['not-consumed'] : []}},
  async mcpServerStatus(){return statuses},
  async reconnectMcpServer(name){
   process.send({type:'reconnect',name});
   await new Promise(resolve=>process.once('message',resolve));
   if(process.argv[1]==='mcp-reconnect-failed')throw new Error('reconnect failed');
  },
  async *[Symbol.asyncIterator](){
   for await(const user of prompt){
    number++;
    const text=user.message.content[0].text;
    process.send({type:'input',text});
    yield {type:'system',subtype:'init',session_id:'native',tools:mcp ? ['Bash','Read','Edit','mcp__target__hold','mcp__healthy__hold'] : client ? ['mcp__functions__lookup'] : options.outputFormat ? ['StructuredOutput'] : [],mcp_servers:client ? [{name:'functions',status:'connected'}] : []};
    if(mcp && text==='hold'){
     assert.deepEqual(await options.hooks.PreToolUse[0].hooks[0]({hook_event_name:'PreToolUse',session_id:'native',tool_use_id:'held',tool_name:'mcp__target__hold',tool_input:{}},'held',{signal:new AbortController().signal}),{});
     const stopped=new Promise(resolve=>{interrupt=resolve});
     process.send({type:'call_admitted'});
     if(process.argv[1]!=='mcp-failed-before-cancel')await Promise.race([stopped,exited]);
     yield {type:'assistant',session_id:'native',parent_tool_use_id:null,message:{content:[{type:'tool_use',id:'held',name:'mcp__target__hold',input:{}}]}};
     yield {type:'user',session_id:'native',parent_tool_use_id:null,message:{content:[{type:'tool_result',tool_use_id:'held',content:'Interrupted',is_error:true}]}};
     if(process.argv[1]==='mcp-failed-before-cancel'){
      process.send({type:'local_call_failed'});
      await Promise.race([stopped,exited]);
     }
    }
    if(mcp && text==='wait'){
     const stopped=new Promise(resolve=>{interrupt=resolve});
     process.send({type:'empty_wait'});
     await Promise.race([stopped,exited]);
    }
    if(client){
     if(text==='pending-function'){
      void client.callTool({name:'lookup',arguments:{text},_meta:{'claudecode/toolUseId':'pending-call'}}).catch(()=>{});
      await Promise.race([new Promise(resolve=>{interrupt=resolve}),exited]);
      if(process.argv[1]==='pending-function-result') yield {type:'user',session_id:'native',parent_tool_use_id:null,message:{content:[{type:'tool_result',tool_use_id:'pending-call',content:'Interrupted',is_error:true}]}};
     }
     if(text==='features'){
      const called=await client.callTool({name:'lookup',arguments:{text},_meta:{'claudecode/toolUseId':'same-native-call'}});
      yield {type:'user',session_id:'native',parent_tool_use_id:null,message:{content:[{type:'tool_result',tool_use_id:'same-native-call',content:called.content,is_error:called.isError}]}};
     }
     for(const event of [
      {type:'message_start',message:{id:'same-native-message'}},
      {type:'content_block_start',index:0,content_block:{type:'text',text}},
      {type:'content_block_stop',index:0},{type:'message_stop'}
     ])yield {type:'stream_event',parent_tool_use_id:null,session_id:'native',event};
    }
    if(options.outputFormat){
     const raw='{"number":9007199254740993}';
     for(const event of [
      {type:'message_start',message:{id:'structured-message'}},
      {type:'content_block_start',index:0,content_block:{type:'text',text:'ordinary prose'}},
      {type:'content_block_stop',index:0},
      {type:'content_block_start',index:1,content_block:{type:'tool_use',id:'structured-call',name:'StructuredOutput',input:{}}},
      {type:'content_block_delta',index:1,delta:{type:'input_json_delta',partial_json:raw}}
     ])yield {type:'stream_event',uuid:crypto.randomUUID(),parent_tool_use_id:null,session_id:'native',event};
     yield {type:'assistant',uuid:'structured-snapshot',session_id:'native',parent_tool_use_id:null,message:{id:'structured-message',content:[{type:'tool_use',id:'structured-call',name:'StructuredOutput',input:JSON.parse(raw)}]}};
     for(const event of [{type:'content_block_stop',index:1},{type:'message_stop'}])yield {type:'stream_event',uuid:crypto.randomUUID(),parent_tool_use_id:null,session_id:'native',event};
     yield {type:'user',uuid:'structured-receipt',session_id:'native',parent_tool_use_id:null,message:{content:[{type:'tool_result',tool_use_id:'structured-call',content:'Structured output provided successfully'}]}};
     if(text==='hold'){
      const interrupted=new Promise(resolve=>{interrupt=resolve});
      process.send({type:'candidate_ready'});
      await Promise.race([interrupted,exited]);
     }
    }
    if(process.argv[1]==='classified'){
     yield {type:'assistant',uuid:'assistant-error',session_id:'native',user_message_uuids:[user.uuid],parent_tool_use_id:null,error:'authentication_failed',message:{content:[]}};
     yield {type:'result',uuid:'error-result',session_id:'native',user_message_uuids:[user.uuid],subtype:'error_during_execution',is_error:true,usage:{input_tokens:1,output_tokens:1},modelUsage:{},total_cost_usd:0};
     continue;
    }
    if(text==='hold' && !options.outputFormat && !mcp) await Promise.race([new Promise(resolve=>{interrupt=resolve}),exited]);
    if(options.abortController.signal.aborted)return;
    yield {type:'result',uuid:'result-'+number,session_id:'native',user_message_uuids:[user.uuid],subtype:'success',is_error:false,result:'answer-'+number,...(options.outputFormat ? {structured_output:{number:9007199254740992}} : {}),usage:{input_tokens:1,output_tokens:1},modelUsage:{fixture:{inputTokens:number,outputTokens:number,costUSD:number/100}},total_cost_usd:number/100};
    interrupt=undefined;
    yield {type:'command_lifecycle',uuid:user.uuid,state:'completed'};
   }
  }
 }}};
};
await import(${JSON.stringify(new URL("../dist/main.js", import.meta.url).href)});
process.disconnect();

`;

async function launch(t,mode="normal") {
 let workspace;
 if(mode.startsWith("mcp-")) {
  const root=mkdtempSync(join(tmpdir(),"claude-mcp-cancel-"));
  t.after(()=>rmSync(root,{recursive:true,force:true}));
  for(const dir of ["home","state","scratch"])mkdirSync(join(root,dir));
  workspace={home:join(root,"home"),state:join(root,"state"),scratch:join(root,"scratch"),env_names:[],network_access:"enabled",mcp:["target","healthy"].map(server_label=>({server_label,command:"/.oac/bin/"+server_label,allowed_tools:null}))};
 }
 const child=spawn(process.execPath,["--input-type=module","-e",fixture,mode],{stdio:["pipe","pipe","pipe","ipc"],env:{...process.env,...(workspace?{HOME:workspace.home,CLAUDE_CONFIG_DIR:workspace.state}:{})}});
 const events=[],observations=[];let buffer="",stderr="";
 child.stdout.setEncoding("utf8");child.stderr.setEncoding("utf8");
 child.stdout.on("data",data=>{buffer+=data;while(buffer.includes("\n")){const end=buffer.indexOf("\n");events.push(JSON.parse(buffer.slice(0,end)));buffer=buffer.slice(end+1)}});
 child.stderr.on("data",data=>{stderr+=data});child.on("message",event=>observations.push(event));
 const closed=new Promise(resolve=>child.once("close",(code,signal)=>resolve({code,signal})));
 t.after(async()=>{child.kill("SIGKILL");await closed});
 const wait=async predicate=>{const until=Date.now()+5000;while(!predicate()){assert.ok(Date.now()<until,JSON.stringify({events,observations,stderr}));await new Promise(resolve=>setTimeout(resolve,5))}};
 const send=value=>child.stdin.write(JSON.stringify(value)+"\n");
 const start=(id,text)=>send({type:"turn_start",turn_id:id,input:[{content:[{type:"input_text",text}]}]});
 send({type:"executor_prepare",preparation_deadline:Date.now()+60000,cwd:"/tmp",model:"fixture",system_prompt:"",
 ...(workspace?{workspace}:{}),
 ...(mode==="structured" ? {output_format:{type:"json_schema",schema:{type:"object",properties:{number:{type:"integer"}}}}} : {}),
 ...(mode==="features" || mode.startsWith("pending-function") ? {functions:[{name:"lookup",description:"lookup",parameters:{type:"object",properties:{text:{type:"string"}}}}]} : {})});
 await wait(()=>events.some(event=>event.type===(mode==="late-ready"?"error":"executor_ready")));
 assert.equal(observations.filter(event=>event.type==="input").length,0);
 return {child,events,observations,closed,wait,send,start};
}

test("executor retains one native process and one Query over two settled Turns",{timeout:10000},async t=>{
 const {child,events,observations,closed,wait,start}=await launch(t);
 for(const id of ["first","second"]){start(id,"answer");await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id===id));assert.equal(events.find(event=>event.type==="turn_settled"&&event.turn_id===id).confirmed,true);assert.equal(events.find(event=>event.type==="turn_settled"&&event.turn_id===id).reusable,true)}
 assert.equal(observations.filter(event=>event.type==="native").length,1);
 assert.equal(observations.filter(event=>event.type==="query").length,1);
 for(const event of events.filter(event=>event.type!=="executor_ready")) assert.ok(["first","second"].includes(event.turn_id));
 assert.equal(events.filter(event=>event.type==="input_ready").length,2);
 const usage=events.filter(event=>event.type==="usage");
 assert.deepEqual(usage.map(event=>[event.turn_id,event.usage.usage.input_tokens]),[["first",1],["second",1]]);
 assert.deepEqual(usage.map(event=>event.usage.modelUsage.fixture.inputTokens),[1,2]);
 assert.deepEqual(usage.map(event=>event.usage.total_cost_usd),[0.01,0.02]);
 for(const event of usage) assert.deepEqual(event.usage.scopes,{usage:"native_turn_main_loop",modelUsage:"query_cumulative",total_cost_usd:"query_cumulative_estimate"});
 child.stdin.end();assert.deepEqual(await closed,{code:0,signal:null});
 assert.equal(observations.filter(event=>event.type==="native_closed").length,1);
});

for (const mode of ["mcp-stop", "mcp-failed-before-cancel", "mcp-reconnect-failed", "mcp-stop-unconfirmed", "mcp-wrong-receipt"]) test(`stdio cancellation ${mode} awaits remote scope and named reconnect before settlement`, {timeout:10000}, async t => {
 const {child,events,observations,closed,wait,send,start}=await launch(t,mode);
 start("first","hold");
 await wait(()=>observations.some(event=>event.type===(mode==="mcp-failed-before-cancel"?"local_call_failed":"call_admitted")));
 send({type:"turn_cancel",turn_id:"first"});
 send({type:"turn_cancel",turn_id:"first"});
 await wait(()=>events.some(event=>event.type==="mcp_stop"));
 assert.deepEqual(events.find(event=>event.type==="mcp_stop"),{type:"mcp_stop",servers:["target"],turn_id:"first"});
 assert.equal(events.some(event=>event.type==="turn_settled"),false);
 assert.equal(observations.some(event=>event.type==="reconnect"),false);
 send({type:"mcp_stopped",turn_id:mode==="mcp-wrong-receipt"?"other":"first",confirmed:mode!=="mcp-stop-unconfirmed"});
 if(mode==="mcp-stop-unconfirmed" || mode==="mcp-wrong-receipt") {
  await wait(()=>events.some(event=>event.type==="turn_settled"));
  assert.equal(events.find(event=>event.type==="turn_settled").confirmed,false);
  assert.equal(events.find(event=>event.type==="turn_settled").reusable,false);
  assert.equal(observations.some(event=>event.type==="reconnect"),false);
  assert.deepEqual(await closed,{code:0,signal:null});
  return;
 }
 await wait(()=>observations.some(event=>event.type==="reconnect"));
 assert.equal(events.some(event=>event.type==="turn_settled"),false);
 assert.deepEqual(observations.filter(event=>event.type==="reconnect"),[{type:"reconnect",name:"target"}]);
 child.send("release-reconnect");
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 const confirmed=mode==="mcp-stop" || mode==="mcp-failed-before-cancel";
 assert.equal(events.find(event=>event.type==="turn_settled").confirmed,confirmed);
 assert.equal(events.find(event=>event.type==="turn_settled").reusable,confirmed);
 assert.equal(observations.filter(event=>event.type==="interrupt").length,1);
 if(confirmed){
  start("second",mode==="mcp-failed-before-cancel"?"wait":"answer");
  send({type:"turn_cancel",turn_id:"first"});
  if(mode==="mcp-failed-before-cancel"){
   await wait(()=>observations.some(event=>event.type==="empty_wait"));
   send({type:"turn_cancel",turn_id:"second"});
  }
  await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id==="second"));
  assert.equal(events.find(event=>event.type==="turn_settled"&&event.turn_id==="second").confirmed,true);
  assert.equal(events.filter(event=>event.type==="mcp_stop").length,1);
  assert.equal(observations.filter(event=>event.type==="native").length,1);
  child.stdin.end();
 }
 assert.deepEqual(await closed,{code:0,signal:null});
});

test("public interrupt settles cancellation and stale cancellation cannot stop the next Turn",{timeout:10000},async t=>{
 const {child,events,observations,closed,wait,send,start}=await launch(t);
 start("first","hold");await wait(()=>events.some(event=>event.type==="input_ready"));
 send({type:"turn_cancel",turn_id:"first"});
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 assert.equal(events.find(event=>event.type==="turn_settled").confirmed,true);
 assert.equal(events.find(event=>event.type==="turn_settled").reusable,true);
 assert.ok(events.some(event=>event.type==="error"&&event.code==="cancelled"&&event.turn_id==="first"));
 start("second","hold");await wait(()=>events.some(event=>event.type==="input_ready"&&event.turn_id==="second"));
 send({type:"turn_cancel",turn_id:"first"});
 await new Promise(resolve=>setTimeout(resolve,20));
 assert.equal(observations.filter(event=>event.type==="interrupt").length,1);
 send({type:"turn_cancel",turn_id:"second"});
 await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id==="second"));
 child.stdin.end();assert.deepEqual(await closed,{code:0,signal:null});
});

for(const mode of ["queued","unknown","rejected"])test(`interrupt ${mode} receipt retires native ownership before non-reusable settlement`,{timeout:10000},async t=>{
 const {events,observations,closed,wait,send,start}=await launch(t,mode);
 start("first","hold");await wait(()=>events.some(event=>event.type==="input_ready"));
 send({type:"turn_cancel",turn_id:"first"});
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 const settlement=events.find(event=>event.type==="turn_settled");
 assert.equal(settlement.confirmed,false);
 assert.equal(settlement.reusable,false);
 assert.equal(settlement.reason,"cancellation_unconfirmed");
 assert.equal(observations.filter(event=>event.type==="native_closed").length,1);
 assert.deepEqual(await closed,{code:0,signal:null});
});

test("reused Turns retain functions, native receipts, steering and message boundaries", {timeout:10000}, async t => {
 const {child,events,closed,wait,send,start}=await launch(t,"features");
 for(const id of ["first","second"]){
  start(id,"features");
  await wait(()=>events.some(event=>event.type==="function_call"&&event.turn_id===id));
  send({type:"steer",turn_id:id,input_id:"same-input-id",input:[{content:[{type:"input_text",text:"extra"}]}]});
  send({type:"function_result",turn_id:id,call_id:"same-native-call",delivery_id:"same-delivery-id",success:true,content:[{type:"input_text",text:"function result"}]});
  await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id===id));
  const own=events.filter(event=>event.turn_id===id);
  assert.equal(own.find(event=>event.type==="turn_settled").confirmed,true);
  assert.equal(own.find(event=>event.type==="turn_settled").reusable,true);
  assert.equal(own.filter(event=>event.type==="function_applied").length,1);
  assert.equal(own.find(event=>event.type==="input_applied").input_id,"same-input-id");
  assert.deepEqual(own.filter(event=>event.type==="output_message"&&event.message.status==="completed").map(event=>event.message.text),["features","extra"]);
  assert.equal(own.filter(event=>event.type==="usage").length,2);
 }
 child.stdin.end();assert.deepEqual(await closed,{code:0,signal:null});
});

for(const mode of ["no-hook-report","false-hook-report"])test("none Executor without requested hooks accepts "+mode,{timeout:10000},async t=>{
 const {child,events,closed,wait,start}=await launch(t,mode);
 start("first","answer");
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 assert.equal(events.find(event=>event.type==="turn_settled").confirmed,true);
 assert.equal(events.find(event=>event.type==="turn_settled").reusable,true);
 child.stdin.end();assert.deepEqual(await closed,{code:0,signal:null});
});


test("executor retains a classified native result without confirming failed settlement",{timeout:10000},async t=>{
 const {events,wait,start}=await launch(t,"classified");
 start("failed-turn","answer");
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 const failure=events.find(event=>event.type==="error");
 assert.equal(failure.turn_id,"failed-turn");
 assert.equal(failure.engine_error_code,"authentication_error");
 assert.equal(failure.session_id,"native");
 assert.equal(failure.result_id,"error-result");
 const settled=events.find(event=>event.type==="turn_settled");
 assert.equal(settled.confirmed,false);
 assert.equal(settled.reusable,false);
});

for (const mode of ["pending-function-result", "pending-function-terminal", "pending-function-unknown"]) test(`unanswered function cancellation ${mode} retains native confirmation requirements`, {timeout:10000}, async t => {
 const {child,events,closed,wait,send,start}=await launch(t,mode);
 start("first","pending-function");
 await wait(()=>events.some(event=>event.type==="function_call"));
 send({type:"turn_cancel",turn_id:"first"});
 await wait(()=>events.some(event=>event.type==="turn_settled"));
 const settled=events.find(event=>event.type==="turn_settled");
 const confirmed=mode!=="pending-function-unknown";
 assert.equal(settled.confirmed,confirmed);
 assert.equal(settled.reusable,confirmed);
 assert.ok(events.some(event=>event.type==="error"&&event.code==="cancelled"));
 assert.equal(events.filter(event=>event.type==="function_applied").length,0);
 if(confirmed){
  start("second","answer");
  await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id==="second"));
  assert.equal(events.find(event=>event.type==="turn_settled"&&event.turn_id==="second").confirmed,true);
  child.stdin.end();
 }
 assert.deepEqual(await closed,{code:0,signal:null});
});

test("an initialization finishing past the original deadline never publishes ready or consumes input",{timeout:10000},async t=>{
 const {events,observations,closed}=await launch(t,"late-ready");
 assert.deepEqual(await closed,{code:0,signal:null});
 assert.equal(events.some(event=>event.type==="executor_ready"),false);
 assert.equal(observations.some(event=>event.type==="input"),false);
 assert.equal(observations.filter(event=>event.type==="native_closed").length,1);
});

test("cancelled structured candidates stay private and a reused Turn preserves raw output and prose identities", {timeout:10000}, async t => {
 const {child,events,observations,closed,wait,send,start}=await launch(t,"structured");
 start("first","hold");
 await wait(()=>observations.some(event=>event.type==="candidate_ready"));
 send({type:"turn_cancel",turn_id:"first"});
 await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id==="first"));
 assert.equal(events.some(event=>event.turn_id==="first"&&event.message?.phase==="final_answer"),false);
 assert.ok(events.some(event=>event.turn_id==="first"&&event.type==="error"&&event.code==="cancelled"));
 start("second","answer");
 await wait(()=>events.some(event=>event.type==="turn_settled"&&event.turn_id==="second"));
 const own=events.filter(event=>event.turn_id==="second");
 assert.deepEqual(own.filter(event=>event.type==="output_message"&&event.message.status==="completed").map(event=>event.message),[
  {id:"structured-message",status:"completed",text:"ordinary prose"},
  {id:"structured-call",status:"completed",phase:"final_answer",text:'{"number":9007199254740993}'}
 ]);
 assert.ok(own.findIndex(event=>event.message?.phase==="final_answer") < own.findIndex(event=>event.type==="result"));
 for(const turn of ["first","second"]){
  const settled=events.find(event=>event.turn_id===turn&&event.type==="turn_settled");
  assert.equal(settled.confirmed,true);
  assert.equal(settled.reusable,true);
 }
 child.stdin.end();assert.deepEqual(await closed,{code:0,signal:null});
});
