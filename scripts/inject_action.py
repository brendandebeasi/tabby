import socket, json, sys, time
sock, kind, win, action = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
msg = {"type": "input",
       "target": {"kind": kind, "window": win},
       "payload": {"type": "action", "action": "press", "button": "left",
                   "resolved_action": action}}
s = socket.socket(socket.AF_UNIX); s.connect(sock)
s.sendall((json.dumps(msg) + "\n").encode()); time.sleep(0.5); s.close()
print("injected", action, "->", win)
