#!/usr/bin/env python3
"""THROW-AWAY spike for #141: read the `/usage` panel of an unmodified `claude` in a PTY.

POSIX only (pty module). Needs `claude` already signed in. Parser checked against Claude Code 2.1.311
(Linux); run with --raw to see the cleaned screen when a new version breaks it.
Usage: python3 scripts/spikes/claude-usage-pty.py [--raw] [--wait SECONDS] [-- EXTRA CLAUDE FLAGS]
"""
import fcntl, os, pty, re, select, struct, sys, termios, time

ANSI = re.compile(rb"\x1b\[[0-9;?<>]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Z0-9]")


FLAGS = ["--settings", '{"disableAllHooks":true}']


def probe(wait_start=8.0, total=30.0, flags=FLAGS):
    pid, fd = pty.fork()
    if pid == 0:
        # Drop markers inherited from a parent Claude Code session, they change its behaviour.
        for k in [k for k in os.environ if k.startswith("CLAUDE_CODE_")]:
            del os.environ[k]
        os.environ["TERM"] = "xterm-256color"
        os.execvp("claude", ["claude", *flags])
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 140, 0, 0))
    buf, t0, sent = b"", time.time(), False
    try:
        while time.time() - t0 < total:
            r, _, _ = select.select([fd], [], [], 0.25)
            if r:
                try:
                    data = os.read(fd, 65536)
                except OSError:
                    break
                if not data:
                    break
                buf += data
            if not sent and time.time() - t0 > wait_start:
                os.write(fd, b"/usage")
                time.sleep(0.3)
                os.write(fd, b"\r")
                sent = True
                t_sent = time.time()
            if sent and time.time() - t_sent > 5:
                break
    finally:
        try:
            os.kill(pid, 9)
            os.waitpid(pid, 0)
        except OSError:
            pass
    return ANSI.sub(b"", buf).decode("utf8", "replace"), time.time() - t0


def parse(screen):
    """Best-effort: a label line followed by '<n>% used' and 'Resets <...>'."""
    out, label = [], None
    for line in screen.splitlines():
        line = line.strip()
        m = re.search(r"(\d{1,3}(?:\.\d+)?)\s*%\s*used", line)
        if m:
            out.append({"label": label, "usedPercent": float(m.group(1)), "resets": None})
        elif out and out[-1]["resets"] is None and re.match(r"Resets?\s*\S", line):
            out[-1]["resets"] = line
        elif line:
            label = line
    return out


if __name__ == "__main__":
    wait = float(sys.argv[sys.argv.index("--wait") + 1]) if "--wait" in sys.argv else 8.0
    flags = FLAGS + sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else FLAGS
    screen, secs = probe(wait, flags=flags)
    if "--raw" in sys.argv:
        print(screen)
    # Offline the panel shows cached numbers: "Showing last-known usage as of 3m ago (could not refresh)".
    stale = re.search(r"could\s*not\s*refresh", screen) is not None
    print(parse(screen), f"stale={stale}", f"({secs:.1f}s)")
