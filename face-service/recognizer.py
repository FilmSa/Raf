"""Exactly-one-face detection, alignment, and normalized SFace embeddings."""
import io
from hashlib import sha256
import threading
import warnings
from pathlib import Path

import cv2
import numpy as np
from PIL import Image, ImageOps, UnidentifiedImageError
from model_manifest import MODELS

MAX_BYTES = 2 * 1024 * 1024
MAX_PIXELS = 4_000_000
Image.MAX_IMAGE_PIXELS = MAX_PIXELS


class FaceError(Exception):
    def __init__(self, status: str):
        super().__init__(status)
        self.status = status


def decode_photo(data: bytes) -> np.ndarray:
    if not data or len(data) > MAX_BYTES:
        raise FaceError("invalid_image")
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            with Image.open(io.BytesIO(data)) as source:
                if source.format not in ("JPEG", "PNG"):
                    raise FaceError("invalid_image")
                width, height = source.size
                if min(width, height) < 32 or width * height > MAX_PIXELS:
                    raise FaceError("invalid_image")
                # Phone portraits often contain EXIF orientation.
                rgb = np.array(ImageOps.exif_transpose(source).convert("RGB"))
    except (UnidentifiedImageError, OSError, ValueError, Image.DecompressionBombError,
            Image.DecompressionBombWarning) as exc:
        raise FaceError("invalid_image") from exc
    return cv2.cvtColor(rgb, cv2.COLOR_RGB2BGR)


class Recognizer:
    def __init__(self, models: Path):
        cv2.setNumThreads(2)
        # Read with pathlib first: OpenCV's native path API fails on Cyrillic
        # Windows paths. The buffer API also works in Linux containers.
        buffers = []
        for filename, metadata in MODELS.items():
            data = (models / filename.split("/")[-1]).read_bytes()
            if len(data) != metadata["size"] or sha256(data).hexdigest() != metadata["sha256"]:
                raise RuntimeError(f"Model integrity check failed: {filename}")
            buffers.append(np.frombuffer(data, dtype=np.uint8))
        detector_bytes, recognizer_bytes = buffers
        empty_config = np.empty(0, dtype=np.uint8)
        self.detector = cv2.FaceDetectorYN.create(
            "onnx", detector_bytes, empty_config, (320, 320),
            0.9, 0.3, 5000,
        )
        self.recognizer = cv2.FaceRecognizerSF.create(
            "onnx", recognizer_bytes, empty_config,
        )
        # OpenCV DNN objects are mutable; serialize access within the worker.
        self.lock = threading.Lock()

    def encode(self, data: bytes) -> list[float]:
        frame = decode_photo(data)
        height, width = frame.shape[:2]
        if max(height, width) > 1280:
            scale = 1280 / max(height, width)
            frame = cv2.resize(frame, (round(width * scale), round(height * scale)))
        with self.lock:
            self.detector.setInputSize((frame.shape[1], frame.shape[0]))
            _, faces = self.detector.detect(frame)
            if faces is None or len(faces) == 0:
                raise FaceError("no_face_detected")
            if len(faces) != 1:
                raise FaceError("multiple_faces")
            crop = self.recognizer.alignCrop(frame, faces[0])
            encoding = self.recognizer.feature(crop).flatten().copy()
        norm = float(np.linalg.norm(encoding))
        if encoding.size != 128 or not np.isfinite(encoding).all() or norm < 1e-12:
            raise FaceError("service_error")
        return (encoding / norm).astype(float).tolist()
