# AgentCore Runtime requires ARM64 images — build with
# `docker buildx build --platform linux/arm64` (see scripts/build-bank-agent.sh),
# not `docker build`, or a build on an x86 host will silently produce a
# non-bootable image.
FROM --platform=linux/arm64 public.ecr.aws/docker/library/python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY agent.py .

EXPOSE 8080

CMD ["python", "agent.py"]
