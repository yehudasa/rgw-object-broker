#!/bin/bash

SCRIPTS_ROOT=$(cd `dirname $0` && pwd)
YAML=${SCRIPTS_ROOT}/yaml

kubectl delete -f $YAML/broker.yaml
helm del --purge broker
make -C ${SCRIPTS_ROOT}/.. push
build_count=`cat ${SCRIPTS_ROOT}/.build`
echo $((build_count+1)) > ${SCRIPTS_ROOT}/.build
helm install $SCRIPTS_ROOT/../chart --name broker --namespace broker
port=`kubectl get svc -n broker broker-rgw-object-broker-node-port | tail -1 | sed 's/.*://g' | sed 's/\/.*//g'`
cat $YAML/broker.yaml.template | sed s/{port}/$port/g > $YAML/broker.yaml
kubectl create -f $YAML/broker.yaml
