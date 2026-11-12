# Build stage: install dependencies into a virtual environment
FROM python:3.13-alpine AS builder

RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt


# Runtime stage
FROM python:3.13-alpine

# Same user/group creation as the v1 image, so existing /db volumes keep their ownership.
RUN addgroup -S appgroup && adduser -S appuser -G appgroup -s /bin/sh && \
    mkdir /db && chown appuser:appgroup /db

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/opt/venv/bin:$PATH" \
    DB_PATH=/db/db.sqlite3

COPY --from=builder /opt/venv /opt/venv

WORKDIR /srv
COPY app ./app

USER appuser
EXPOSE 8000
VOLUME /db

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/health', timeout=3)"

# A single worker: the API keeps one shared connection to Uptime Kuma.
CMD ["uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8000", "--proxy-headers"]
