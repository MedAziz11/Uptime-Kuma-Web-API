# First stage: Build environment
FROM python:3.9-alpine AS builder

# Create a new user and group to run the app under, the user should have a shell
RUN addgroup -S appgroup && adduser -S appuser -G appgroup -s /bin/sh

# Set the working directory to /app and give appuser ownership
WORKDIR /app
RUN chown appuser:appgroup /app

# Set environment variables
ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONUNBUFFERED 1


# Create a virtual environment and activate it
RUN python -m venv venv
ENV PATH="/app/venv/bin:$PATH"

# Upgrade pip and install the requirements
COPY --chown=appuser:appgroup ./requirements.txt ./


RUN pip install --upgrade pip && \
    pip install --no-cache-dir -r ./requirements.txt
       
COPY --chown=appuser:appgroup ./app /app

USER appuser


# Second stage: Runtime environment
FROM python:3.9-alpine

# Create a new user and group to run the app under, the user should have a shell
RUN addgroup -S appgroup && adduser -S appuser -G appgroup -s /bin/sh

# Copy Python packages and the virtual environment from the builder stage
COPY --from=builder --chown=appuser:appgroup /app /app

# Set the working directory to /app
WORKDIR /app

# Set environment variables
ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONUNBUFFERED 1

# Set the PATH to include the virtual environment
ENV PATH="/app/venv/bin:$PATH"

## Cleanup without impacting the codebase
RUN chmod +x /app/entrypoint.sh 

USER appuser

#Run the app
ENTRYPOINT ["/app/entrypoint.sh"]

# ENTRYPOINT [ "tail", "-f", "/dev/null" ]


