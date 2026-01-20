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
json_return_value=$(docker exec "$pgcontainer" ./scripts/postgres/query_json_return.sh "$db" "$base64_string") 
echo -n "$json_return_value"