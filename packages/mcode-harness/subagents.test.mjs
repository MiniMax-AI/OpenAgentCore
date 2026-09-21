import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { mkdtempSync, mkdirSync, rmSync, symlinkSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { enforceSubagentAdmission } from './native-subagent-admission.mjs';
import { readSubagentSnapshot } from './subagent-snapshot.mjs';

function schema(db) {
  db.exec(`CREATE TABLE local_runtime_sessions(session_id TEXT PRIMARY KEY,parent_session_id TEXT,created_at_ms INTEGER,agent_name TEXT,session_kind TEXT,purpose TEXT,status TEXT,archived INTEGER);
    CREATE TABLE local_runtime_background_tasks(task_id TEXT,owner_session_id TEXT,kind TEXT,status TEXT,created_at_ms INTEGER,record_json TEXT);
    CREATE TABLE local_runtime_turn_ingress(turn_id TEXT,session_id TEXT,status TEXT,accepted_at_ms INTEGER,completed_at_ms INTEGER);
    CREATE TABLE local_runtime_message_rows(id INTEGER PRIMARY KEY,session_id TEXT,msg_id TEXT,turn_id TEXT,role TEXT,created_at_ms INTEGER,data_json TEXT);`);
  const add=db.prepare('INSERT INTO local_runtime_sessions VALUES(?,?,1000,\'worker\',\'task\',NULL,\'idle\',0)');
  add.run('root',null);add.run('child','root');add.run('nested','child');add.run('foreign',null);
}

test('native admission counts nested work, isolates roots and reclaims terminal capacity', () => {
  const db=new DatabaseSync(':memory:');schema(db);
  const create=(id,owner,limit) => {
    db.exec('BEGIN IMMEDIATE');
    try {
      enforceSubagentAdmission(db,{kind:'subagent',ownerSessionId:owner},limit);
      db.prepare('INSERT INTO local_runtime_background_tasks VALUES(?,?,\'subagent\',\'running\',1000,\'{}\')').run(id,owner);
      db.exec('COMMIT');
    } catch(error) {db.exec('ROLLBACK');throw error;}
  };
  try {
    create('a','root',1);
    assert.throws(()=>create('b','nested',1),/limit reached/);
    create('foreign-task','foreign',1);
    db.exec("UPDATE local_runtime_background_tasks SET status='canceled' WHERE task_id='a'");
    db.exec(`UPDATE local_runtime_background_tasks SET record_json='{"metadata":{"childSessionId":"child","subTurnId":"still-running"}}' WHERE task_id='a';
      INSERT INTO local_runtime_turn_ingress VALUES('still-running','child','accepted',1000,NULL)`);
    assert.throws(()=>create('too-early','nested',1),/limit reached/);
    db.exec("UPDATE local_runtime_turn_ingress SET status='aborted',completed_at_ms=1100 WHERE turn_id='still-running'");
    create('after-cancel','nested',1);
    assert.throws(()=>create('disabled','root',0),/disabled/);
    assert.throws(()=>create('unknown','missing',6),/ownership/);
    assert.equal(db.prepare('SELECT count(*) AS n FROM local_runtime_background_tasks').get().n,3);
  } finally {db.close();}
});

test('protected snapshots are complete, child-owned and stable after reopening', () => {
  const dir=realpathSync(mkdtempSync(join(tmpdir(),'mcode-subagents-')));mkdirSync(join(dir,'v2/sqlite'),{recursive:true});
  const db=new DatabaseSync(join(dir,'v2/sqlite/runtime-state.sqlite'));schema(db);
  db.exec("INSERT INTO local_runtime_turn_ingress VALUES('turn','child','completed',1100,1300)");
  db.prepare('INSERT INTO local_runtime_message_rows VALUES(1,?,?,?,?,?,?)').run('child','answer','turn','assistant',1200,JSON.stringify({msg_content:'answer'}));
  db.prepare('INSERT INTO local_runtime_message_rows VALUES(2,?,?,?,?,?,?)').run('child','input','turn','user',1100,JSON.stringify({msg_content:'input'}));
  db.close();
  try {
    const first=readSubagentSnapshot(dir,'root');
    assert.equal(first.complete,true);
    assert.deepEqual(first.sessions.map(s=>s.id).sort(),['child','nested','root']);
    assert.deepEqual(first.sessions.find(s=>s.id==='child').messages.map(m=>m.id),['input','answer']);
    assert.equal(first.sessions.find(s=>s.id==='root').messages.length,0);
    assert.deepEqual(readSubagentSnapshot(dir,'root'),first);
    assert.throws(()=>readSubagentSnapshot(dir,'missing'),/not found/);
    symlinkSync(join(dir,'v2/sqlite/runtime-state.sqlite'),join(dir,'linked'));
    assert.throws(()=>readSubagentSnapshot(join(dir,'linked'),'root'));
  } finally {rmSync(dir,{recursive:true,force:true});}
});
