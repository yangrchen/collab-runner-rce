#!/bin/bash

WORKER_PATH="$(pwd)/bin/worker"

sudo go build -o "$WORKER_PATH" ./services/worker

if [[ $? -eq 0 ]]; then
    echo "Worker successfully built."
fi

sudo env "PATH=$PATH" "$WORKER_PATH"