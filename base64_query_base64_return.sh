#!/bin/bash

db="$1"
if [ -z "$db" ]; then
  echo "No db string passed"
  exit
fi

base64_string="$2"
if [ -z "$base64_string" ]; then
  echo "No base64 string passed"
  exit
fi

pgcontainer="$PGCONTAINER"
base64_return_value=$(docker exec "$pgcontainer" ./scripts/postgres/base64_query_base64_return.sh "$db" "$base64_string") 
echo -n "$base64_return_value"

