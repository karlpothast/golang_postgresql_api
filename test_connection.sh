#!/bin/bash

# postgresd container must be running

#pg_conn="postgresql://admin:admin123@$ip:5432/inventory;"
docker exec -i postgresd bash -c 'pg_isready -d "$pg_conn"'

# working connectoin msg
# /var/run/postgresql:5432 - accepting connections