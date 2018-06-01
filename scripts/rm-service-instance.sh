#!/bin/bash

SCRIPTS_ROOT=`dirname $0`
YAML=${SCRIPTS_ROOT}/yaml

kubectl delete -f $YAML/instance.yaml 
