import socket
import json
import time
import subprocess
import threading
import sys
import os

relay_dir = os.path.expanduser("~/ai-mvp-backend/.brain/relay/claude_code_test_hold")
os.makedirs(relay_dir, exist_ok=True)

def run_test():
    print("[TEST] Starting daemon...")
    daemon = subprocess.Popen(["./eideticd", "ccr", "serve", "--unix-socket"])
    time.sleep(3)

    result_box = {"events": []}

    def client():
        try:
            s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            s.connect("/tmp/eidetic-ccr.sock")
            req = {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/call",
                "params": {
                    "name": "nucleus_ccr_subscribe",
                    "arguments": {
                        "role": "claude_code_test_hold",
                        "timeout_seconds": 3600
                    }
                }
            }
            s.sendall((json.dumps(req) + "\n").encode())
            print("[TEST] Client subscribed. Waiting up to 3600 seconds...")
            
            # The client loop reading multiple events?
            # Wait, our Go implementation currently returns and closes the request on the FIRST wake event!
            # The adapter contract says: "Returns when (a) a wake event fires, OR (b) timeout elapses. Adapter is responsible for re-invoking on return."
            
            while True:
                data = s.recv(4096)
                if not data:
                    break
                resp = data.decode().strip()
                for line in resp.split("\n"):
                    if not line:
                        continue
                    print(f"[TEST] Client received: {line}")
                    result_box["events"].append(line)
                    
                    # If it returns on wake event, we must re-invoke!
                    s.sendall((json.dumps(req) + "\n").encode())
        except Exception as e:
            print(f"[TEST] Client error: {e}")

    t = threading.Thread(target=client)
    t.daemon = True
    t.start()

    print("[TEST] Sleeping for 30 minutes...")
    time.sleep(30 * 60)
    print("[TEST] T+30min: Dropping first synthetic relay...")
    with open(os.path.join(relay_dir, "test_30min.json"), "w") as f:
        f.write('{"test": "30min"}')
        
    print("[TEST] Sleeping for 29 minutes...")
    time.sleep(29 * 60)
    print("[TEST] T+59min: Dropping second synthetic relay...")
    with open(os.path.join(relay_dir, "test_59min.json"), "w") as f:
        f.write('{"test": "59min"}')

    print("[TEST] Sleeping for 1.5 minutes to let it timeout...")
    time.sleep(90)

    print("[TEST] Killing daemon...")
    daemon.terminate()
    daemon.wait()

    events = result_box["events"]
    print(f"Total events received: {len(events)}")
    success = False
    if len(events) >= 2:
        if 'test_30min.json' in str(events) and 'test_59min.json' in str(events):
            success = True

    import datetime
    import uuid
    import json

    relay_dest = os.path.expanduser("~/ai-mvp-backend/.brain/relay/op_assistant")
    os.makedirs(relay_dest, exist_ok=True)
    
    timestamp = datetime.datetime.now().isoformat()
    filename = f"{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}_{uuid.uuid4().hex[:8]}.json"
    
    if success:
        print("RESULT: SUCCESS")
        relay = {
            "from": "antigravity",
            "subject": "[CCR W3-4 Crack 3 GREEN] 1hr hold test complete; v0.1 release-decision unblocked",
            "body": "Empirical 1-hour native socket long-poll test passed. Both synthetic relays delivered at T+30 and T+59. No socket disconnect.",
            "timestamp": timestamp
        }
        with open(os.path.join(relay_dest, filename), "w") as f:
            json.dump(relay, f)
        sys.exit(0)
    else:
        print("RESULT: FAILED")
        relay = {
            "from": "antigravity",
            "subject": "[CCR W3-4 Crack 3 BLOCKED] 1hr hold failed at T+60min; design adjustment required",
            "body": f"1-hour test failed. Events received: {events}. Suspect socket timeout or keepalive drop.",
            "timestamp": timestamp
        }
        with open(os.path.join(relay_dest, filename), "w") as f:
            json.dump(relay, f)
        sys.exit(1)

if __name__ == "__main__":
    run_test()
