# Builds the EzhikLB node agent as a static-ish Linux binary, then packages
# it with the userspace tools it shells out to (ipvsadm, iptables, ip,
# iputils-ping, conntrack) — the same tool set scripts/install-node.sh
# installs for a bare-metal node. Run this container with:
#
#   docker run -d --name ezhiklb-node --restart unless-stopped \
#     --network host --cap-add NET_ADMIN --cap-add NET_RAW --cap-add NET_BROADCAST \
#     -v /lib/modules:/lib/modules:ro \
#     -v ezhiklb-node-state:/var/lib/ezhiklb-agent \
#     ezhiklb-node-agent:latest
#
# --network host is required: IPVS/iptables manage the *host's* network
# namespace, not a container-private one. The IPVS/conntrack kernel modules
# themselves must already be loaded on the host before the container starts
# (an unprivileged container cannot modprobe the host kernel) — see
# scripts/install-node.sh's --docker path, which does this for you.

FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY node-agent/go.mod ./
RUN go mod download 2>/dev/null || true
COPY node-agent/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ezhiklb-agent ./cmd/ezhiklb-agent

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ipvsadm iptables iproute2 iputils-ping conntrack ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ezhiklb-agent /usr/local/bin/ezhiklb-agent
ENV EZHIKLB_AGENT_STATE=/var/lib/ezhiklb-agent/state.json \
    EZHIKLB_AGENT_ENROLL_DIR=/var/lib/ezhiklb-agent/enroll \
    EZHIKLB_AGENT_HOST=0.0.0.0 \
    EZHIKLB_AGENT_PORT=62050
VOLUME ["/var/lib/ezhiklb-agent"]
ENTRYPOINT ["/usr/local/bin/ezhiklb-agent"]
