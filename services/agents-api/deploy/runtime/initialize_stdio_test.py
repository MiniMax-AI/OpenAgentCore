"""Real stdio lifetime checks: python3 -I -S in a disposable packaged Runtime.
Requires writable /environment roots and the deployed nested-sandbox profile.
No native harness or model is involved.
"""
import json
import os, sys
from pathlib import Path
import runpy
import select, signal
import subprocess
import tempfile, threading, time

HELPER = '/usr/local/bin/oac-runtime-initialize'
SELF = ['/usr/bin/python3', '-I', '-S', str(Path(__file__).resolve())]
WRITER = "import sys,time\nfor n in range(300):\n with open(sys.argv[1],'ab') as f: f.write(b'x')\n time.sleep(.1)\n"
SERVER = """import subprocess,sys
subprocess.Popen([sys.executable,'-I','-S','-c',sys.argv[2],sys.argv[1]],
                 start_new_session=True,stdin=subprocess.DEVNULL,
                 stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
for line in sys.stdin:
    print(line.rstrip(),flush=True)
"""

def until(check):
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(.01)
    raise AssertionError('Process condition did not settle within five seconds')

def exited(fd):
    poll = select.poll()
    poll.register(fd, select.POLLIN)
    assert poll.poll(5000), 'Owned process survived'

def close_process(fd):
    if fd is not None:
        try:
            signal.pidfd_send_signal(fd, signal.SIGKILL)
        except ProcessLookupError:
            pass
        os.close(fd)

def guard(directory, mode):
    runtime = runpy.run_path(HELPER)
    workspace = '/workspace/' + directory.name
    if mode == 'startup':
        # Stop the actual forked child before its parent binding, without
        # replacing Popen, prctl, exec or any production process operation.
        def stop_before_binding(frame, event, arg):
            if event == 'call' and frame.f_code.co_name == 'bind_parent':
                sys.settrace(None)
                (directory / 'stopped').write_text(str(os.getpid()))
                os.kill(os.getpid(), signal.SIGSTOP)
            return stop_before_binding
        sys.settrace(stop_before_binding)
        command = ['/bin/sh', '-c', 'echo escaped > ' + workspace + '/escaped']
    elif mode.startswith('exit'):
        command = ['/bin/sh', '-c', 'exit ' + mode[4:]]
    else:
        command = ['/usr/bin/python3', '-I', '-S', '-c', SERVER, workspace + '/ticks', WRITER]
    return runtime['stdio_lifetime'](runtime['sandbox']('enabled', '/workspace') + command)

def native_parent(directory):
    signal.signal(signal.SIGUSR1, lambda *_: sys.exit(0))
    receipt = {}
    def start():
        receipt['creator_tid'] = threading.get_native_id()
        child = subprocess.Popen(SELF + ['guard', str(directory), 'server'])
        receipt['wrapper_pid'] = child.pid
        until(lambda: (directory / 'ticks').exists())  # Exit the thread only after bwrap is active.
    thread = threading.Thread(target=start)
    thread.start()
    thread.join()
    until(lambda: not Path('/proc', str(os.getpid()), 'task', str(receipt['creator_tid'])).exists())
    (directory / 'ready').write_text(json.dumps(receipt))
    signal.pause()

def parent_exit(directory, death):
    parent = subprocess.Popen(SELF + ['parent', str(directory)], stdin=subprocess.PIPE,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    wrapper_fd = None
    try:
        until(lambda: (directory / 'ready').exists() and (directory / 'ready').stat().st_size)
        receipt = json.loads((directory / 'ready').read_text())
        wrapper_fd = os.pidfd_open(receipt['wrapper_pid'])
        ticks = directory / 'ticks'
        until(lambda: ticks.exists() and ticks.stat().st_size >= 3)
        parent.stdin.write(b'creator-thread-exited\n')
        parent.stdin.flush()
        assert select.select([parent.stdout], [], [], 5)[0], 'Inherited stdout closed or stalled'
        assert parent.stdout.readline() == b'creator-thread-exited\n'
        before = ticks.stat().st_size
        until(lambda: ticks.stat().st_size > before)
        parent.send_signal(death)
        assert parent.wait(timeout=5) == (0 if death == signal.SIGUSR1 else -signal.SIGKILL)
        exited(wrapper_fd)
        time.sleep(.2)
        stopped = ticks.read_bytes()
        time.sleep(.4)
        assert ticks.read_bytes() == stopped, 'Detached sandbox descendant survived parent exit'
    finally:
        if parent.poll() is None:
            parent.kill()
        parent.wait(timeout=5)
        close_process(wrapper_fd)
        for stream in (parent.stdin, parent.stdout, parent.stderr):
            stream.close()

def startup_kill(directory):
    wrapper = subprocess.Popen(SELF + ['guard', str(directory), 'startup'])
    child_fd = None
    try:
        until(lambda: (directory / 'stopped').exists() and (directory / 'stopped').stat().st_size)
        child = int((directory / 'stopped').read_text())
        child_fd = os.pidfd_open(child)
        until(lambda: '\nState:\tT' in Path('/proc', str(child), 'status').read_text())
        wrapper.kill()
        wrapper.wait(timeout=5)
        signal.pidfd_send_signal(child_fd, signal.SIGCONT)
        exited(child_fd)
        assert not (directory / 'escaped').exists(), 'Child executed after launcher died before exec'
    finally:
        if wrapper.poll() is None:
            wrapper.kill()
        wrapper.wait(timeout=5)
        close_process(child_fd)

def main():
    for name in ('workspace', 'packages', 'initialization'):
        Path('/environment', name).mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='stdio-lifetime-', dir='/environment/workspace') as work:
        directory = Path(work)
        for code in (0, 7):
            result = subprocess.run(SELF + ['guard', work, 'exit' + str(code)], capture_output=True, timeout=5)
            assert result.returncode == code and not result.stdout, 'Immediate exit or stdout changed'
        for death in (signal.SIGUSR1, signal.SIGKILL):
            for name in ('ready', 'ticks'):
                (directory / name).unlink(missing_ok=True)
            parent_exit(directory, death)
        startup_kill(directory)
    print(json.dumps({'stdio_lifetime': 'passed', 'creator_thread_exit': True,
                      'parent_normal_and_kill': True, 'immediate_exit': True, 'preexec_launcher_kill': True}))

if __name__ == '__main__':
    if len(sys.argv) > 1:
        sys.exit(guard(Path(sys.argv[2]), sys.argv[3]) if sys.argv[1] == 'guard' else native_parent(Path(sys.argv[2])))
    main()
