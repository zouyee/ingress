#!/bin/bash
rm -f main
go run *.go  --etcd "10.142.21.201:2379" --auth "10.142.21.201:2379" --image "10.142.21.201:2379" --port "8084"#!/bin/bash
