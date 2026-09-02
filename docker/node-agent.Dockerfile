# Packages the already-built EzhikLB node-agent Linux binary with the
# userspace tools it shells out to (ipvsadm, iptables, ip, iputils-ping,
# conntrack) — the same tool set scripts/install-node.sh installs for a
# bare-metal node. Does NOT build the binary from Go source: the build
# context must already contain it at bin/ezhiklb-agent — either downloaded
# from a GitHub Release (scripts/bootstrap-node.sh --docker does this for
# you) or built locally with:
#
#   cd node-agent && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
#     go build -trimpath -ldflags="-s -w" -o ../bin/ezhiklb-agent ./cmd/ezhiklb-agent
#
# Run with:
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

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ipvsadm iptables iproute2 iputils-ping conntrack ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY bin/ezhiklb-agent /usr/local/bin/ezhiklb-agent
RUN chmod 0755 /usr/local/bin/ezhiklb-agent
ENV EZHIKLB_AGENT_STATE=/var/lib/ezhiklb-agent/state.json \
    EZHIKLB_AGENT_ENROLL_DIR=/var/lib/ezhiklb-agent/enroll \
    EZHIKLB_AGENT_HOST=0.0.0.0 \
    EZHIKLB_AGENT_PORT=62050
VOLUME ["/var/lib/ezhiklb-agent"]
ENTRYPOINT ["/usr/local/bin/ezhiklb-agent"]
