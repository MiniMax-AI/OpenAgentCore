import test from "node:test";
import assert from "node:assert/strict";
import { StructuredOutput } from "../dist/structured_output.js";
import { parseStart } from "../dist/request.js";
const call = (id, extra={}) => ({type:'assistant', session_id:'session', parent_tool_use_id:null, message:{content:[{type:'tool_use',id,name:'StructuredOutput',input:{}}]},...extra});
const receipt = (id,error=false) => ({type:'user',session_id:'session',parent_tool_use_id:null,message:{content:[{type:'tool_result',tool_use_id:id,is_error:error}]}});
const result = {type:'result',subtype:'success',is_error:false,structured_output:{number:9007199254740992},result:'{"number":9007199254740993}'};
test('only the native confirmed terminal output is published, without JSON reserialization',()=>{
 const o=new StructuredOutput();
 o.consume(call('retry'),'session');o.consume(receipt('retry',true),'session');
 o.consume(call('final'),'session');assert.throws(()=>o.complete(result));
 o.consume(receipt('final'),'session');
 assert.deepEqual(o.complete(result),{type:'output_message',message:{id:'final',status:'completed',phase:'final_answer',text:result.result}});
 assert.throws(()=>o.complete(result));
});
test('unrelated or failed results cannot publish a candidate',()=>{
 for(const extra of [{isReplay:true},{isSynthetic:true},{parent_tool_use_id:'child'},{session_id:'other'}]){
  const o=new StructuredOutput();o.consume(call('id',extra),'session');o.consume(receipt('id'),'session');assert.throws(()=>o.complete(result));
 }
 for(const r of [{...result,subtype:'error_max_structured_output_retries'},{...result,structured_output:undefined},{...result,is_error:true}]){
  const o=new StructuredOutput();o.consume(call('id'),'session');o.consume(receipt('id'),'session');assert.throws(()=>o.complete(r));
 }
});
test('output configuration is restricted to the qualified profile',()=>{
 const r={type:'start',prompt:'hello',model:'model',system_prompt:'',cwd:'/tmp',observe_messages:true,output_format:{type:'json_schema',schema:{type:'object'}}};
 assert.deepEqual(parseStart(JSON.stringify(r)),r);
 for(const change of [{observe_messages:false},{subagents:{max_concurrent:1}},{mcp_http_servers:[]},{output_format:{type:'text',schema:{}}}]) assert.throws(()=>parseStart(JSON.stringify({...r,...change})));
});
