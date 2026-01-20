#!/bin/bash

pgcontainer="$PGCONTAINER"
return_value=$(docker exec "$pgcontainer" ./scripts/postgres/list_dbs.sh) 
echo -n "$return_value"