"""Interactive, isolated tmux peer for browser wheel round trips (JSON lines)."""

import base64
import fcntl
import json
import os
import pty
import re
import select
import shlex
import struct
import subprocess
import sys
import termios
import time
import tty
import uuid


def pane(mode):
    tty.setraw(0)
    if mode == "inline-mouse":
        os.write(1, ("".join(f"original-{line:03}\r\n" for line in range(80)) + "\x1b[?1000h\x1b[?1006h").encode())
        while os.read(0, 4096):
            pass
    else:
        first = 56
        os.write(1, b"\x1b[?1049h\x1b[?1h")
        if mode == "alternate-mouse":
            os.write(1, b"\x1b[?1000h\x1b[?1006h")

        def draw():
            os.write(1, ("\x1b[H\x1b[2J" + "\r\n".join("Back to bottom" if row == 23 and first < 56 else f"original-{first + row:03}" for row in range(24))).encode())

        draw()
        pending = b""
        while True:
            pending += os.read(0, 4096)
            if not pending:
                break
            while len(pending) >= 3:
                if pending.startswith(b"\x1b[<"):
                    mouse = re.match(br"\x1b\[<(\d+);(\d+);(\d+)[Mm]", pending)
                    if not mouse:
                        break
                    pending = pending[mouse.end():]
                    if mouse[1] == b"64":
                        first = max(0, first - 1)
                    elif mouse[1] == b"65":
                        first = min(56, first + 1)
                    elif mouse[1] == b"0" and mouse[3] == b"24":
                        first = 56
                    draw()
                    continue
                # Conversation UIs reserve arrows for the composer. Page keys
                # scroll their retained transcript by half a viewport.
                length = 4 if pending.startswith((b"\x1b[5", b"\x1b[6")) else 3
                if len(pending) < length:
                    break
                key, pending = pending[:length], pending[length:]
                if key == b"\x1b[5~":
                    first = max(0, first - 12)
                elif key == b"\x1b[6~":
                    first = min(56, first + 12)
                draw()


if len(sys.argv) > 1 and sys.argv[1] == "--pane":
    pane(sys.argv[2])
    sys.exit()

mode = sys.argv[1]
socket = "velin-wheel-test-" + uuid.uuid4().hex[:12]
pid = master = None


def tmux(*args):
    return subprocess.check_output(["tmux", "-L", socket, *args], stderr=subprocess.PIPE)


try:
    tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "80", "-y", "24",
         f"exec python3 {os.path.abspath(__file__)} --pane {mode}")
    tmux("set-option", "-t", "test", "status", "off")
    pid, master = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "xterm-256color"
        os.execvp("tmux", ["tmux", "-L", socket, "attach-session", "-t", "test"])
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))

    def reply(event=None):
        chunks = []
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            readable, _, _ = select.select([master], [], [], 0.1)
            if not readable:
                break
            chunks.append(os.read(master, 65536))
        size, alternate = tmux("display-message", "-p", "-t", "test", "#{history_size} #{alternate_on}").split()
        print(json.dumps({"data": base64.b64encode(b"".join(chunks)).decode(), "historySize": int(size),
                          "alternateScreen": alternate == b"1", "event": event}), flush=True)

    reply()
    for line in sys.stdin:
        message = json.loads(line)
        event = None
        if message["type"] == "input":
            os.write(master, base64.b64decode(message["data"]))
        elif message["type"] in ("scroll_application", "click_application"):
            row, column = message["row"], message["column"]
            if message["type"] == "scroll_application":
                lines = message["lines"]
                data = f"\x1b[<{64 if lines < 0 else 65};{column};{row}M" * abs(lines)
            else:
                data = f"\x1b[<0;{column};{row}M\x1b[<0;{column};{row}m"
            command = shlex.join(["send-keys", "-H", "-t", "test", *[f"{b:02x}" for b in data.encode()]])
            tmux("if-shell", "-F", "-t", "test", "#{&&:#{alternate_on},#{==:#{pane_in_mode},0}}", command)
        elif message["type"] == "scroll_history_to":
            position, end = message["position"], message["historySize"]
            if position >= end:
                tmux("send-keys", "-X", "-t", "test", "cancel")
            else:
                tmux("copy-mode", "-t", "test")
                tmux("send-keys", "-X", "-t", "test", "history-top")
                if position:
                    tmux("send-keys", "-X", "-N", str(position), "-t", "test", "scroll-down")
            event = {**message, "type": "history_position"}
        reply(event)
finally:
    subprocess.run(["tmux", "-L", socket, "kill-server"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if master is not None:
        os.close(master)
    if pid:
        os.waitpid(pid, 0)
