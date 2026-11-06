#!/usr/bin/env python3
"""Start/stop this checkout only, on localhost; never print login credentials."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
STATE = ROOT / '.local-run'
PID = STATE / 'server.pid'
URL = 'http://127.0.0.1:3032/music'

def healthy():
    try:
        with urllib.request.urlopen(URL + '/healthz', timeout=1) as response:
            return response.status == 200 and b'go-music-dl' in response.read()
    except Exception:
        return False

if 'stop' in sys.argv[1:]:
    if PID.exists():
        pid = int(PID.read_text().strip())
        command = subprocess.run(['/bin/ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True).stdout
        if str(ROOT / 'music-dl') in command and '--port 3032' in command:
            os.kill(pid, signal.SIGTERM)
            print('已停止此文件夹的 go-music-dl。')
        else:
            print('服务已停止，或 PID 不属于本项目；未终止其他进程。')
        PID.unlink(missing_ok=True)
    else:
        print('没有找到本项目的运行记录。')
    sys.exit(0)

os.chdir(ROOT)
STATE.mkdir(exist_ok=True, mode=0o700)
if not healthy():
    # Refresh only the existing official client's session, locally.
    result = subprocess.run(['go', 'run', '.local-run/configure-local.go'], capture_output=True)
    if result.returncode:
        print('未能刷新汽水客户端登录，继续使用此前的本地配置。')
    env = os.environ.copy()
    env.update({
        'QISHUI_SIGNER_URL': '',
        'QISHUI_SIGNER_TOKEN': '',
        'QISHUI_CLIENT_DIR': '/Volumes/Elements2/Applications/汽水音乐.app/Contents',
        'QISHUI_BDMS_PATH': '/Volumes/Elements2/Applications/汽水音乐.app/Contents/Resources/app.asar.unpacked/bdms.node',
        'QISHUI_IDENTITY_FILE': str(STATE / 'soda-identity.json'),
    })
    descriptor = os.open(STATE / 'server.log', os.O_CREAT | os.O_APPEND | os.O_WRONLY, 0o600)
    with os.fdopen(descriptor, 'ab') as log:
        process = subprocess.Popen([str(ROOT / 'music-dl'), 'web', '--host', '127.0.0.1', '--port', '3032', '--no-browser'], cwd=ROOT, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
    PID.write_text(str(process.pid) + '\n')
    for _ in range(40):
        if healthy():
            break
        if process.poll() is not None:
            break
        time.sleep(0.25)
    if not healthy():
        print('启动失败，请检查 .local-run/server.log（包含本地管理初始化信息，请勿公开）。')
        sys.exit(1)
print('go-music-dl 已运行：' + URL)
if '--no-open' not in sys.argv:
    subprocess.run(['/usr/bin/open', URL], check=False)
