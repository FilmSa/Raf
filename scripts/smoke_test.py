"""Real-network integration test. Starts isolated Go and SFace servers."""
import argparse
import http.cookiejar
import io
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, build_opener, HTTPCookieProcessor, urlopen

from PIL import Image
from access_client import multipart

ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def wait_ready(url, process):
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError(f"Service exited with code {process.returncode}")
        try:
            with urlopen(url + "/health", timeout=1):
                return
        except (URLError, OSError):
            time.sleep(0.2)
    raise RuntimeError("Service startup timeout")


def stop_process(process):
    if process.poll() is not None:
        return
    if os.name == "nt":
        # A venv's Windows python.exe may redirect through a child process.
        subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    else:
        process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def fixture(name):
    cached = ROOT / ".runtime" / "fixtures" / name
    if not cached.exists():
        cached.parent.mkdir(parents=True, exist_ok=True)
        url = "https://raw.githubusercontent.com/opencv/opencv/master/samples/data/" + name
        with urlopen(url, timeout=60) as response:
            cached.write_bytes(response.read())
    return cached.read_bytes()


def run(server_binary):
    (ROOT / ".runtime").mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="smoke-", dir=ROOT / ".runtime") as temporary:
        directory = Path(temporary)
        face_port, go_port = free_port(), free_port()
        face_url, go_url = f"http://127.0.0.1:{face_port}", f"http://127.0.0.1:{go_port}"
        password = "integration-test-password"
        env = dict(os.environ, APP_ADDR=f"127.0.0.1:{go_port}", DB_PATH=str(directory / "test.db"),
                   ADMIN_PASSWORD=password, FACE_SERVICE_URL=face_url, DEVICE_API_KEY="", COOKIE_SECURE="false", FACE_THRESHOLD="0.363")
        processes = []
        with (directory / "face.log").open("w") as face_log, (directory / "go.log").open("w") as go_log:
            try:
                face = subprocess.Popen([sys.executable, "-m", "uvicorn", "app:app", "--app-dir", str(ROOT / "face-service"), "--host", "127.0.0.1", "--port", str(face_port), "--no-access-log"], cwd=ROOT, env=env, stdout=face_log, stderr=face_log)
                processes.append(face); wait_ready(face_url, face)
                server = subprocess.Popen([str(server_binary)], cwd=ROOT, env=env, stdout=go_log, stderr=go_log)
                processes.append(server); wait_ready(go_url, server)
                cookies = http.cookiejar.CookieJar()
                opener = build_opener(HTTPCookieProcessor(cookies))

                def request(path, data=None, method=None, content_type="application/json", expected=200):
                    if isinstance(data, dict):
                        data = json.dumps(data).encode()
                    req = Request(go_url + path, data, {"Content-Type":content_type}, method=method)
                    try:
                        response = opener.open(req, timeout=25)
                    except HTTPError as exc:
                        response = exc
                    with response:
                        result = json.load(response)
                        assert response.code == expected, (path, response.code, result)
                        return result

                request("/api/admin/login", {"password":password})
                portrait = fixture("lena.jpg")
                other = fixture("messi5.jpg")
                uid = "04A1B2C3D4E5F6"
                body, mime = multipart({"full_name":"Тестовый сотрудник", "card_uid":uid},portrait)
                user = request("/api/admin/users",body,content_type=mime,expected=201)
                assert "face_encoding" not in user
                request("/api/admin/users",body,content_type=mime,expected=409)

                def access(card, photo, status):
                    body, mime = multipart({"uid":card},photo)
                    result = request("/api/v1/access",body,content_type=mime)
                    assert result == {"access":status == "success", "status":status}, result
                    print("PASS",status)

                # Different JPEG compression: real embeddings, not byte equality.
                stream = io.BytesIO()
                with Image.open(io.BytesIO(portrait)) as image:
                    image.save(stream,format="JPEG",quality=80)
                access(uid,stream.getvalue(),"success")
                access("00000000000000",portrait,"unknown_uid")
                access(uid,other,"face_mismatch")
                stream = io.BytesIO();Image.new("RGB",(640,480)).save(stream,format="JPEG")
                access(uid,stream.getvalue(),"no_face_detected")
                with Image.open(io.BytesIO(portrait)) as image:
                    group = Image.new("RGB",(1024,512));group.paste(image,(0,0));group.paste(image,(512,0))
                    stream = io.BytesIO();group.save(stream,format="JPEG")
                access(uid,stream.getvalue(),"multiple_faces")
                request(f"/api/admin/users/{user['id']}",{"is_active":False},method="PATCH")
                access(uid,portrait,"inactive_user")
                request(f"/api/admin/users/{user['id']}",{"is_active":True},method="PATCH")
                stop_process(face)
                access(uid,portrait,"service_unavailable")
                request(f"/api/admin/users/{user['id']}",method="DELETE")
                logs = request("/api/admin/logs")
                assert logs["total"] == 7, logs
                assert any(item["full_name"] == "Тестовый сотрудник" for item in logs["items"])
                assert request("/api/admin/users") == []
                print("PASS registration, duplicate, block/unblock, deletion, durable audit")
                print("Real SFace integration: all checks passed")
            except Exception:
                for output in (directory / "face.log", directory / "go.log"):
                    print(output.name, output.read_text(errors="replace"), file=sys.stderr)
                raise
            finally:
                for process in reversed(processes):
                    stop_process(process)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--server-binary",type=Path,default=ROOT / "bin" / ("server.exe" if os.name == "nt" else "server"))
    run(parser.parse_args().server_binary.resolve())
