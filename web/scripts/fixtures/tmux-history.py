"""Record real tmux PTY output for the TerminalPane browser regression test."""

import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import tempfile
import termios
import time
import uuid


socket = "velin-history-test-" + uuid.uuid4().hex[:12]


def tmux(*args):
    return subprocess.check_output(["tmux", "-L", socket, *args], stderr=subprocess.PIPE)


with tempfile.TemporaryDirectory(prefix="velin-history-") as directory:
    fifo = os.path.join(directory, "output")
    os.mkfifo(fifo)
    output = os.open(fifo, os.O_RDWR)
    pid = master = None
    try:
        tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "80", "-y", "24", "exec cat " + fifo)
        tmux("set-option", "-t", "test", "status", "off")
        pid, master = pty.fork()
        if pid == 0:
            os.environ["TERM"] = "xterm-256color"
            os.execvp("tmux", ["tmux", "-L", socket, "attach-session", "-t", "test"])
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))

        def drain():
            chunks = []
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                readable, _, _ = select.select([master], [], [], 0.15)
                if not readable:
                    break
                chunks.append(os.read(master, 65536))
            return b"".join(chunks).decode()

        stages = [{"name": "attach", "data": drain()}]

        def write(name, text):
            os.write(output, text.encode())
            stages.append({"name": name, "data": drain()})

        write("shell-history", "".join(f"original-{line:03}\r\n" for line in range(80)))
        write("alternate-screen", "\x1b[?1049h\x1b[2J\x1b[H")
        for frame in range(3):
            write("TUI-redraw", "\x1b[H" + "".join(f"temporary-{frame}-{line:02}\r\n" for line in range(30)))
        write("restore-shell", "\x1b[?1049l")
        capture = tmux("capture-pane", "-p", "-t", "test", "-S", "-").decode()
        assert "temporary-" not in capture, "TUI redraws must stay out of shell history"
        for line in range(80):
            assert capture.count(f"original-{line:03}") == 1

        for cycle in range(3):
            tmux("copy-mode", "-t", "test")
            tmux("send-keys", "-X", "-t", "test", "history-top")
            stages.append({"name": f"history-top-{cycle}", "data": drain(), "firstLine": 0})
            tmux("send-keys", "-X", "-N", "30", "-t", "test", "scroll-down")
            stages.append({"name": f"history-down-{cycle}", "data": drain(), "firstLine": 30})
            tmux("send-keys", "-X", "-N", "10", "-t", "test", "scroll-up")
            stages.append({"name": f"history-up-{cycle}", "data": drain(), "firstLine": 20})
            tmux("send-keys", "-X", "-t", "test", "cancel")
            stages.append({"name": f"live-screen-{cycle}", "data": drain(), "firstLine": 57})
        print(json.dumps({"stages": stages}))
    finally:
        subprocess.run(["tmux", "-L", socket, "kill-server"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        os.close(output)
        if master is not None:
            os.close(master)
        if pid:
            os.waitpid(pid, 0)
