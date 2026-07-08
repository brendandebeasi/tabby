import socket, json, sys, subprocess, time
L="tbtest"
def tmux(*a):
    return subprocess.check_output(["tmux","-L",L,*a]).decode().strip()
sess = tmux("display-message","-p","-t","t","#{session_id}")
sock_path = f"/tmp/tabby-daemon-{sess}.sock"
panes = tmux("list-panes","-t","t","-F","#{pane_id} #{pane_start_command}").splitlines()
content=None
for p in panes:
    if "render " not in p:
        content=p.split()[0]; break
print("panes:", *["  "+p for p in panes], sep="\n")
if not content: print("NO CONTENT PANE"); sys.exit(1)
w0 = int(tmux("display-message","-p","-t",content,"#{pane_width}"))
print(f"content={content} width_before={w0} sock={sock_path}")
msg={"type":"input",
     "target":{"kind":"pane-border","pane":content,"edge":"right"},
     "payload":{"type":"action","action":"motion","button":"left",
                "resolved_action":"drag_resize","resolved_target":content,
                "drag_edge":"right","drag_dx":-6,"pane_id":content}}
s=socket.socket(socket.AF_UNIX); s.connect(sock_path)
s.sendall((json.dumps(msg)+"\n").encode()); time.sleep(0.5); s.close(); time.sleep(0.5)
w1=int(tmux("display-message","-p","-t",content,"#{pane_width}"))
print(f"width_after={w1}  delta={w1-w0}  (right-edge dx=-6 => narrower by ~6)")
