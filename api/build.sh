#!/bin/bash
cd "$(dirname "$0")"
GOROOT=/usr/lib/go-1.18 go build -o cicy-code-api ./mgr
