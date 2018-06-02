#!/bin/bash

SCRIPTS_ROOT=`dirname $0`
YAML=${SCRIPTS_ROOT}/yaml

ip=`kubectl config view --minify=true -ojsonpath='{.clusters[0].cluster.server}' | sed 's/.*\///g' | sed 's/:.*//g'`
port=`kubectl get svc -n broker broker-rgw-object-broker-node-port -o jsonpath={.'spec'.'ports'[0].'nodePort'}`
broker_addr="$ip:$port"

cat $YAML/broker.yaml.template | sed s/{addr}/$broker_addr/g > $YAML/broker.yaml

echo Updated broker address to $broker_addr
