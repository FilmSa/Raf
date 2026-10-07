"""ESP32-compatible request sender with no third-party dependencies."""
import argparse
import json
import secrets
from pathlib import Path
from urllib.request import Request, urlopen


def multipart(fields: dict[str, str], photo: bytes, filename: str = "capture.jpg") -> tuple[bytes, str]:
    boundary = "----AccessControl" + secrets.token_hex(12)
    parts = []
    for key, value in fields.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
    parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="photo"; filename="{filename}"\r\nContent-Type: image/jpeg\r\n\r\n'.encode())
    parts.extend((photo, f"\r\n--{boundary}--\r\n".encode()))
    return b"".join(parts), f"multipart/form-data; boundary={boundary}"


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Send uid + JPEG to the access API")
    parser.add_argument("--server", default="http://127.0.0.1:8080")
    parser.add_argument("--uid", required=True)
    parser.add_argument("--photo", required=True, type=Path)
    parser.add_argument("--key", default="")
    args = parser.parse_args()
    body, content_type = multipart({"uid": args.uid}, args.photo.read_bytes())
    headers = {"Content-Type": content_type}
    if args.key:
        headers["X-Device-Key"] = args.key
    request = Request(args.server.rstrip("/") + "/api/v1/access", body, headers)
    try:
        with urlopen(request, timeout=25) as response:
            result = json.load(response)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        raise SystemExit(0 if result.get("access") is True else 2)
    except OSError as exc:
        parser.exit(1, f"Connection error: {exc}\n")
