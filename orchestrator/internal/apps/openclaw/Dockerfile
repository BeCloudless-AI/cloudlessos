# Cloudless build of OpenClaw, pre-configured to use Cloudless AI (vLLM).
# OpenClaw is distributed as an npm package (Node 24).
FROM node:24-slim

RUN npm install -g openclaw@latest

# Bake the Cloudless AI provider config (see internal/apps/openclaw/openclaw.json).
RUN mkdir -p /root/.openclaw
COPY openclaw.json /root/.openclaw/openclaw.json

# Referenced as ${CUSTOM_API_KEY} in the config; vLLM ignores the value.
ENV CUSTOM_API_KEY=cloudless

EXPOSE 18789
# Local single-user appliance, run with host networking: bind the gateway to
# loopback (so it accepts --auth none — the 0.0.0.0 guard only blocks no-auth on
# non-loopback) at the host's 127.0.0.1:18789. Plug-and-play: nothing to enter.
CMD ["openclaw", "gateway", "run", "--auth", "none", "--bind", "loopback", "--port", "18789"]
