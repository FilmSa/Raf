FROM python:3.11-slim
WORKDIR /app
COPY requirements.lock.txt .
RUN pip install --no-cache-dir -r requirements.lock.txt
COPY download_models.py model_manifest.py ./
RUN python download_models.py --directory /app/models
COPY app.py recognizer.py ./
RUN useradd --create-home appuser
USER appuser
EXPOSE 8001
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8001", "--workers", "1", "--no-access-log"]
