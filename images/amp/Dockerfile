# The sandbox image for the docker backend. It holds the agent and the tools an
# agent reaches for, and nothing that belongs to a specific repository.
FROM debian:bookworm-slim

ARG AMP_VERSION=""

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git openssh-client procps ripgrep jq less \
      build-essential python3 \
 && rm -rf /var/lib/apt/lists/*

ENV AMP_HOME=/opt/amp
ENV PATH=/opt/amp/bin:$PATH

RUN AMP_VERSION="${AMP_VERSION}" bash -c 'curl -fsSL https://ampcode.com/install.sh | bash'

WORKDIR /workspace
CMD ["sleep", "infinity"]
