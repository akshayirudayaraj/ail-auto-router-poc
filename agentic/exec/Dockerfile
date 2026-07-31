# Hermetic pytest runner for the executable agentic task set.
# Built once (agentic-runner:py311); tasks run offline inside it so the
# execution oracle needs no network at run time.
FROM python:3.11-slim
RUN pip install --no-cache-dir pytest==8.3.3
WORKDIR /work
