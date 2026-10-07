"""Private face encoder. Public access decisions belong to the Go server."""
import asyncio
import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool

from recognizer import FaceError, MAX_BYTES, Recognizer

logger = logging.getLogger("face-service")


@asynccontextmanager
async def lifespan(app: FastAPI):
    models = Path(os.environ.get("MODEL_DIR", str(Path(__file__).parent / "models")))
    app.state.recognizer = Recognizer(models)
    app.state.capacity = asyncio.Semaphore(4)
    yield


app = FastAPI(title="Access Control face encoder", version="1.0.0", lifespan=lifespan,
              docs_url=None, redoc_url=None, openapi_url=None)


@app.get("/health")
def health():
    return {"status": "ok", "model": "sface-2021dec", "dimensions": 128}


@app.post("/encode")
async def encode(request: Request):
    data = bytearray()
    async for block in request.stream():
        if len(data) + len(block) > MAX_BYTES:
            return {"status": "invalid_image"}
        data.extend(block)
    try:
        await asyncio.wait_for(request.app.state.capacity.acquire(), timeout=2)
    except TimeoutError:
        return JSONResponse({"status": "service_error"}, status_code=503)
    try:
        encoding = await run_in_threadpool(request.app.state.recognizer.encode, bytes(data))
        return {"status": "ok", "encoding": encoding}
    except FaceError as exc:
        return {"status": exc.status}
    except Exception:
        logger.exception("Face inference failed")
        return {"status": "service_error"}
    finally:
        request.app.state.capacity.release()
