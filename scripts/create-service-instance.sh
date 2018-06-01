#!/bin/bash

SCRIPTS_ROOT=`dirname $0`
YAML=${SCRIPTS_ROOT}/yaml

count=`cat ${SCRIPTS_ROOT}/.count`
count=$((count+1))

echo $count > ${SCRIPTS_ROOT}/.count
cat $YAML/instance.yaml.template | sed s/{count}/$count/g > $YAML/instance.yaml
kubectl create -f $YAML/instance.yaml 

bind_count=1
echo $bind_count > ${SCRIPTS_ROOT}/.bindcount
cat $YAML/bind.yaml.template | sed s/{count}/$count/g | sed s/{bind}/$bind_count/g > $YAML/bind.yaml
