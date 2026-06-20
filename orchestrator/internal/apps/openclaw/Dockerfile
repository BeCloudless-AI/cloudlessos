# Cloudless build of OpenClaw, pre-configured to use Cloudless AI (vLLM).
# OpenClaw is distributed as an npm package (Node 24).
FROM node:24-slim

RUN npm install -g openclaw@latest

# Bake the Cloudless AI provider config (see internal/apps/openclaw/openclaw.json).
RUN mkdir -p /root/.openclaw
COPY openclaw.json /root/.openclaw/openclaw.json

# Referenced as ${CUSTOM_API_KEY} in the config; vLLM ignores the value.
ENV CUSTOM_API_KEY=cloudless
# In a container the gateway binds 0.0.0.0 and requires auth; set a default token.
ENV OPENCLAW_GATEWAY_TOKEN=cloudless

EXPOSE 18789
CMD ["openclaw", "gateway", "--port", "18789"]
