import path from 'node:path'
import { createHash } from 'node:crypto'

export const sessions = new Map<string,string>()
const endpoint = () => (process.env.CHAINPROOF_URL ?? 'http://127.0.0.1:7331').replace(/\/$/,'')
const sha256 = (value:string) => createHash('sha256').update(value).digest('hex')
async function request(route:string, init:RequestInit={}) { const response=await fetch(endpoint()+route,{...init,headers:{'content-type':'application/json',...init.headers}});if(!response.ok)throw new Error(`ChainProof ${response.status}: ${await response.text()}`);return response.json() }
async function putArtifact(content:string,mediaType:string){const hash=sha256(content);const response=await fetch(`${endpoint()}/api/artifacts/${hash}`,{method:'PUT',headers:{'content-type':mediaType},body:content});if(!response.ok)throw new Error(`ChainProof artifact ${response.status}`);return {hash,media_type:mediaType} }
async function append(run:string,kind:string,payload:unknown,event:OpenClawEvent,artifacts:unknown[]=[]){return request(`/api/runs/${run}/events`,{method:'POST',body:JSON.stringify({kind,source:{adapter:'openclaw',mode:'reported',native_event_id:`${event.type}:${event.action}:${event.timestamp?.toISOString()??Date.now()}`},payload,artifacts})})}

export interface OpenClawEvent { type:string;action:string;sessionKey:string;timestamp?:Date;content?:string;toolName?:string;params?:unknown;result?:unknown;context?:{workspaceDir?:string;cfg?:Record<string,unknown>} }

export default async function handler(event:OpenClawEvent):Promise<void>{
  try {
    const eventName=`${event.type}:${event.action}`
    if(eventName==='command:new'){
      const agent=path.basename(event.context?.workspaceDir??'openclaw')
      const run=await request('/api/runs',{method:'POST',body:JSON.stringify({agent,harness:'openclaw',metadata:{session_key_hash:sha256(event.sessionKey)}})}) as {run_id:string}
      sessions.set(event.sessionKey,run.run_id)
      await append(run.run_id,'run.started',{workspace:path.basename(event.context?.workspaceDir??'')},event)
      return
    }
    const run=sessions.get(event.sessionKey);if(!run)return
    if(eventName==='message:received'){const content=event.content??'',artifacts=process.env.CHAINPROOF_STORE_CONTENT==='true'?[await putArtifact(content,'text/plain; charset=utf-8')]:[];await append(run,'human.input',{content_hash:sha256(content)},event,artifacts);return}
    if(event.action==='after_tool_call'){const input=JSON.stringify(event.params??{}),output=JSON.stringify(event.result??{}),artifacts=process.env.CHAINPROOF_STORE_CONTENT==='true'?[await putArtifact(input,'application/json'),await putArtifact(output,'application/json')]:[];await append(run,'tool.result',{tool:event.toolName??'unknown',input_hash:sha256(input),output_hash:sha256(output)},event,artifacts);return}
    if(eventName==='message:sent'){const content=event.content??'',artifacts=process.env.CHAINPROOF_STORE_CONTENT==='true'?[await putArtifact(content,'text/plain; charset=utf-8')]:[];await append(run,'model.output',{content_hash:sha256(content)},event,artifacts);return}
    if(eventName==='command:stop'||eventName==='command:reset'){sessions.delete(event.sessionKey);await request(`/api/runs/${run}/complete`,{method:'POST',body:JSON.stringify({status:event.action==='stop'?'completed':'cancelled'})})}
  } catch(error) { console.error('[chainproof-openclaw]',error) }
}
