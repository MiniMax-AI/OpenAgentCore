import { spawn } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';
import { isIP } from 'node:net';
import { dirname, join, isAbsolute, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const profile = JSON.parse(readFileSync(process.argv[2], 'utf8'));
if (!isAbsolute(profile.workspace) || normalize(profile.workspace) !== profile.workspace || (process.argv[3] && profile.workspace !== process.argv[3]))
  throw new Error('Workspace profile does not match execution binding');
if (profile.network !== 'enabled') throw new Error('Invalid network policy');
const domains=profile.allowedDomains??[];
const hostname=/^(?=.{1,253}$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i;
if (!Array.isArray(domains) || (profile.network==='restricted'
 ? domains.length<1 || domains.length>100 || domains.some(host=>typeof host!=='string'||!hostname.test(host)||host.trim()!==host||isIP(host)!==0)
 : domains.length!==0)) throw new Error('Invalid network domains');
const baseEnv = {...process.env};
if (Object.hasOwn(profile, 'toolEnv')) throw new Error('Inline tool environment is unsupported');
if (profile.toolEnvFile !== undefined) {
 // Resolve values only at tool launch, from the Runtime-owned frozen snapshot.
 // Never include parser diagnostics: malformed input can contain credentials.
 let values;
 try {
  const path = profile.toolEnvFile;
  if (typeof path !== 'string' || !isAbsolute(path) || normalize(path) !== path) throw new Error();
  const raw = readFileSync(path);
  if (raw.length > 1024 * 1024) throw new Error();
  values = JSON.parse(raw.toString('utf8'));
  if (!values || typeof values !== 'object' || Array.isArray(values) ||
      Object.entries(values).some(([key, value]) => !key || /[=\x00\r\n]/.test(key) || typeof value !== 'string' || value.includes('\0'))) throw new Error();
 } catch { throw new Error('Runtime tool environment unavailable'); }
 Object.assign(baseEnv, values);
}
if (profile.capabilityRoot && (typeof profile.capabilityRoot !== 'string' || !isAbsolute(profile.capabilityRoot) || normalize(profile.capabilityRoot) !== profile.capabilityRoot || profile.capabilityRoot === '/' || /[\\\x00-\x1f\x7f]/.test(profile.capabilityRoot))) throw new Error('Invalid capability root');
if (profile.skills && !profile.capabilityRoot) throw new Error('Missing capability root');
let child;
let cancelled=false;
const cancel=()=>{cancelled=true;child?.kill('SIGTERM');};
process.on('SIGTERM',cancel);process.on('SIGINT',cancel);
try {
 mkdirSync(profile.scratch,{recursive:true});
 if (cancelled) throw new Error('Cancelled before workspace tool start');
 child=spawn(process.execPath,[join(here,'dist/worker.mjs'),profile.workspace],{cwd:profile.workspace,env:baseEnv,stdio:['pipe','pipe','pipe']});
 process.stdin.pipe(child.stdin);child.stdout.pipe(process.stdout);child.stderr.pipe(process.stderr);
 child.stdin.on('error',()=>cancel());
 const status=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('close',resolve);});
 process.exitCode=cancelled?1:(status??1);
} catch(error) {process.stderr.write(String(error)+'\n');process.exitCode=1;}
