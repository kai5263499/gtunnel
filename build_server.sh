#!/bin/bash

docker build --network host -f gserver/Dockerfile --target gtunserver-prod . -t kai5263499/gtunnel-server

if test $? -eq 0
then
    docker rm gtunnel-server -f || true
else
    echo "[!] Failed to  build gtunnel server image"
    exit 1
fi

docker create --net host -v $PWD/logs:/logs -v $PWD/tls:/tls --name gtunnel-server kai5263499/gtunnel-server

if test $? -eq 0
then
    echo "[*] Docker container successfully created"
else
    echo "[!] Failed to create gtunnel-server container"
fi
