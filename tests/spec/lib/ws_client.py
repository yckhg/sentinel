"""최소 WebSocket 관찰 클라이언트 (표준 라이브러리만 사용, 구독/관찰 전용 — 서버 상태 변경 없음).

사용: python ws_client.py <host> <port> <path> <timeout_sec> <mode>
  mode=normal : 서버 ping에 pong 응답
  mode=noping : ping에 응답하지 않음 (서버측 idle 종료 관찰용)

출력(줄 단위):
  HTTP: <업그레이드 응답 상태줄>
  TEXT: <수신 텍스트 프레임>
  PING: <t초>
  CLOSE: <t초>
  EOF: <t초>
  TIMEOUT_END: <t초>
"""
import base64
import os
import socket
import struct
import sys
import time

host, port, path, timeout, mode = (
    sys.argv[1], int(sys.argv[2]), sys.argv[3], float(sys.argv[4]), sys.argv[5]
)

key = base64.b64encode(os.urandom(16)).decode()
req = (
    f"GET {path} HTTP/1.1\r\nHost: {host}\r\n"
    "Upgrade: websocket\r\nConnection: Upgrade\r\n"
    f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
)
s = socket.create_connection((host, port), timeout=10)
s.sendall(req.encode())

buf = b""
while b"\r\n\r\n" not in buf:
    chunk = s.recv(4096)
    if not chunk:
        break
    buf += chunk
head, _, rest = buf.partition(b"\r\n\r\n")
status = head.split(b"\r\n")[0].decode(errors="replace")
print(f"HTTP: {status}", flush=True)
if "101" not in status:
    sys.exit(0)

start = time.time()
data = rest
s.settimeout(1.0)


def elapsed():
    return f"{time.time() - start:.1f}s"


def fill(n):
    global data
    while len(data) < n:
        try:
            chunk = s.recv(4096)
        except socket.timeout:
            if time.time() - start > timeout:
                print(f"TIMEOUT_END: {elapsed()}", flush=True)
                sys.exit(0)
            continue
        if not chunk:
            print(f"EOF: {elapsed()}", flush=True)
            sys.exit(0)
        data += chunk


def send_frame(opcode, payload=b""):
    mask = os.urandom(4)
    header = bytes([0x80 | opcode])
    n = len(payload)
    if n < 126:
        header += bytes([0x80 | n])
    elif n < 65536:
        header += bytes([0x80 | 126]) + struct.pack(">H", n)
    else:
        header += bytes([0x80 | 127]) + struct.pack(">Q", n)
    masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    s.sendall(header + mask + masked)


while True:
    fill(2)
    b0, b1 = data[0], data[1]
    opcode = b0 & 0x0F
    plen = b1 & 0x7F
    off = 2
    if plen == 126:
        fill(4)
        plen = struct.unpack(">H", data[2:4])[0]
        off = 4
    elif plen == 127:
        fill(10)
        plen = struct.unpack(">Q", data[2:10])[0]
        off = 10
    fill(off + plen)
    payload = data[off:off + plen]
    data = data[off + plen:]
    if opcode == 0x1:
        print("TEXT: " + payload.decode(errors="replace"), flush=True)
    elif opcode == 0x9:
        print(f"PING: {elapsed()}", flush=True)
        if mode != "noping":
            send_frame(0xA, payload)
    elif opcode == 0x8:
        print(f"CLOSE: {elapsed()}", flush=True)
        sys.exit(0)
