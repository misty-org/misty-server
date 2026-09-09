import { beforeEach, expect, it, vi } from "vitest";
const fixture=vi.hoisted(()=>({name:"sdk.planner",capability:"tasks.create",confirmed:true,completions:[] as Record<string,unknown>[]}));
vi.mock("workflow",()=>({getWorkflowMetadata:()=>({workflowRunId:"runtime"}),FatalError:class extends Error{},RetryableError:class extends Error{},defineHook:()=>({})}));
vi.mock("../src/control-plane.js",()=>({controlPlaneRequest:async(_context:unknown,operation:string,body:Record<string,unknown>)=>{
 if(operation==="context")return {model_id:"fixture/model",system:"",prompt:"Create the task",allowed_tools:[fixture.name],required_tools:["tasks.create"]};
 if(operation==="budget")return {version:1,remaining_ms:1800000,active:true,deadline:new Date(Date.now()+1800000).toISOString()};
 if(operation==="complete")fixture.completions.push(body);
 return {accepted:true};
}}));
vi.mock("../src/mcp-runtime.js",()=>({
 discoverRemoteMCPTools:async()=>({supported:true,tools:[{name:fixture.name,capability:fixture.capability,description:"Create a task in the resolved destination",inputSchema:{type:"object",properties:{}}}]}),
 requestMCPToolExecution:async()=>({result:fixture.confirmed?{status:"success",result:{taskReference:"verified-task"},evidence:[{reference:"verified-task"}],partial:false}:{status:"uncertain",reason:"Response lost"}}),
}));
vi.mock("@ai-sdk/workflow",()=>({WorkflowAgent:class{
 constructor(private options:any){}
 async stream(){
  const [name,tool]=Object.entries(this.options.tools).find(([name])=>name!=="misty_discover_capabilities") as [string,any];
  const call={toolCallId:"create",toolName:name,input:{}};
  await this.options.onToolExecutionStart({toolCall:call});
  const output=await tool.execute({}, {toolCallId:"create"});
  await this.options.onToolExecutionEnd({toolCall:call,success:true,durationMs:1,output});
  return {steps:[{text:"Task created",content:[{type:"tool-result",toolName:name,output}]}],messages:[],finishReason:"stop",totalUsage:{inputTokens:1,outputTokens:1}};
 }
}}));
import {runSpaceTaskAgent} from "../workflows/space-task-agent.js";
beforeEach(()=>{fixture.completions=[];fixture.capability="tasks.create";fixture.confirmed=true});
for(const provider of ["planner","todoist","fixture"]){it(`accepts verified ${provider} execution for the same required semantic action`,async()=>{
 fixture.name=`sdk.${provider}`;
 await runSpaceTaskAgent({mistyRunId:"run-pilot",controlPlaneURL:"https://api.test"});
 expect(fixture.completions[0]).toMatchObject({status:"success"});
})}
it("does not satisfy a required action with an uncertain provider result",async()=>{
 fixture.confirmed=false;
 await runSpaceTaskAgent({mistyRunId:"run-pilot",controlPlaneURL:"https://api.test"});
 expect(fixture.completions[0]?.status).toBe("incomplete");
});
it("does not satisfy task creation with a different successful capability",async()=>{
 fixture.capability="inbox.draft";
 await runSpaceTaskAgent({mistyRunId:"run-pilot",controlPlaneURL:"https://api.test"});
 expect(fixture.completions[0]).toMatchObject({status:"incomplete",error_code:"required_action_not_completed"});
});
