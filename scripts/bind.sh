#!/bin/bash

SCRIPTS_ROOT=`dirname $0`
YAML=${SCRIPTS_ROOT}/yaml

count=`cat ${SCRIPTS_ROOT}/.count`
bind_count=`cat ${SCRIPTS_ROOT}/.bindcount`
bind_count=$((bind_count+1))

echo $bind_count > ${SCRIPTS_ROOT}/.bindcount
cat $YAML/bind.yaml.template | sed s/{count}/$count/g | sed s/{bind}/$bind_count/g > $YAML/bind.yaml

kubectl create -f $YAML/bind.yaml 
