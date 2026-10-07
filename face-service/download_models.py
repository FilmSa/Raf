"""Download official OpenCV Zoo models and verify pinned SHA-256 identities."""
from hashlib import sha256
from pathlib import Path
from urllib.request import urlopen
import argparse
from model_manifest import MODELS
MEDIA = "https://media.githubusercontent.com/media/opencv/opencv_zoo/main/models/"


def download(target: Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    for model, metadata in MODELS.items():
        name = model.split("/")[-1]
        checksum, size = metadata["sha256"], metadata["size"]
        destination = target / name
        if destination.exists() and sha256(destination.read_bytes()).hexdigest() == checksum:
            print(f"Verified: {name}")
            continue
        temporary = destination.with_suffix(".download")
        try:
            digest = sha256()
            received = 0
            with urlopen(MEDIA + model, timeout=120) as response, temporary.open("wb") as file:
                while block := response.read(1024 * 1024):
                    received += len(block)
                    digest.update(block)
                    file.write(block)
            if received != size or digest.hexdigest() != checksum:
                raise RuntimeError(f"Model checksum mismatch: {name}")
            temporary.replace(destination)
            print(f"Downloaded and verified: {name} ({size:,} bytes)")
        finally:
            temporary.unlink(missing_ok=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", type=Path, default=Path(__file__).parent / "models")
    download(parser.parse_args().directory)
