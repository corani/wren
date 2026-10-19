#!/bin/bash

prog=$(realpath "$0") || exit 1
root=$(dirname "$prog")
rc=0

function log_info { echo "[INFO] $*"; }
function log_error { echo "[ERRO] $*" >&2; }

function do_echo {
  echo "[CMD ] $*"
  local code
  TIMEFORMAT="[TIME] took %3lR"
  if time "$@"; then
    return 0
  else
    code=$?
    if [ "$rc" -eq 0 ]; then
      rc=$code
    fi
    log_error "return code $code"
    return "$code"
  fi
}

function usage {
  log_info "Usage: $(basename "$prog") [-h] [-b] [-t] [-v] [-l] [-g] [-i]"
  log_info "  -h, help:    Show this help message"
  log_info "  -b, build:   Build bin/wren"
  log_info "  -t, test:    Run Go tests"
  log_info "  -v, vet:     Run go vet"
  log_info "  -l, lint:    Run golangci-lint"
  log_info "  -g, gen:     Run go fix and format with gofumpt"
  log_info "  -i, install: Install wren"
  log_info "Pass multiple actions as separate arguments, e.g. -g -t -v -l -b."
}

cd "$root" || exit 1

if [ "$#" -eq 0 ]; then
  usage
fi

while [ "$#" -gt 0 ]; do
  arg=$1
  shift

  case "$arg" in
  -h | help)
    usage
    ;;
  -b | build)
    do_echo mkdir -p bin/
    do_echo go build -o bin/wren ./cmd/wren
    ;;
  -t | test)
    do_echo go test ./...
    ;;
  -v | vet)
    do_echo go vet ./...
    ;;
  -l | lint)
    do_echo go tool golangci-lint run
    ;;
  -g | gen)
    do_echo go fix ./... && do_echo go tool gofumpt -w -extra .
    ;;
  -i | install)
    do_echo go install ./cmd/wren
    ;;
  *)
    log_error "Unknown argument $arg"
    usage
    exit 1
    ;;
  esac
done

exit "$rc"
