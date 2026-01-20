#!/bin/bash

pgcontainer="$PGCONTAINER"
return_value=$(docker exec "$pgcontainer" ./scripts/postgres/version.sh) 
echo -n "$return_value"