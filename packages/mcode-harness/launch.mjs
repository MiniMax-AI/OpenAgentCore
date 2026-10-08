import { spawn } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join, isAbsolute, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const profile = JSON.parse(readFileSync(process.argv[2], 'utf8'));
if (!isAbsolute(profile.workspace) || normalize(profile.workspace) !== profile.workspace) throw new Error('Invalid workspace profile');
let child;
let cancelled=false;
const cancel=()=>{cancelled=true;child?.kill('SIGTERM');};
process.on('SIGTERM',cancel);process.on('SIGINT',cancel);
try {
 mkdirSync(profile.scratch,{recursive:true});
 if (cancelled) throw new Error('Cancelled before workspace tool start');
 child=spawn(process.execPath,[join(here,'dist/worker.mjs'),profile.workspace],{cwd:profile.workspace,env:process.env,stdio:['pipe','pipe','pipe']});
 process.stdin.pipe(child.stdin);child.stdout.pipe(process.stdout);child.stderr.pipe(process.stderr);
 child.stdin.on('error',()=>cancel());
 const status=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('close',resolve);});
 process.exitCode=cancelled?1:(status??1);
} catch(error) {process.stderr.write(String(error)+'\n');process.exitCode=1;}
