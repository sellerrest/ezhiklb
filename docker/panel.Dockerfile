# Builds the EzhikLB panel (Python/FastAPI control plane + its React
# frontend) as a single image. This is an *alternative* to the systemd/venv
# path scripts/install-panel.sh sets up — pick one, not both.
#
# Run with docker-compose.yml at the repo root, or directly:
#
#   docker run -d --name ezhiklb-panel --restart unless-stopped \
#     -p 127.0.0.1:8080:8080 \
#     -v ezhiklb-panel-data:/var/lib/ezhiklb \
#     -e EZHIKLB_DATABASE_URL=sqlite+aiosqlite:////var/lib/ezhiklb/ezhiklb.db \
#     ezhiklb-panel:latest
#
# Publishing only to 127.0.0.1 (as above) keeps the same "no public admin
# port" posture the systemd install recommends — put it behind an SSH
# tunnel or a reverse proxy you control, not directly on the internet.

FROM node:20-bookworm-slim AS web-build
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app/panel
COPY panel/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY panel/ ./
COPY --from=web-build /src/dist /app/web/dist

ENV EZHIKLB_HOST=0.0.0.0 \
    EZHIKLB_PORT=8080 \
    EZHIKLB_WEB_DIR=/app/web/dist \
    EZHIKLB_DATABASE_URL=sqlite+aiosqlite:////var/lib/ezhiklb/ezhiklb.db \
    EZHIKLB_SECURE_COOKIE=0
VOLUME ["/var/lib/ezhiklb"]
EXPOSE 8080
ENTRYPOINT ["python", "-m", "ezhiklb_panel.main"]
