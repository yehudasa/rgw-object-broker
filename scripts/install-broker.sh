#!/bin/bash

#
# This script assumes service catalog and service broker are running on the
# same kubernetes cluster.
#

SCRIPTS_ROOT=$(cd `dirname $0` && pwd)
YAML=${SCRIPTS_ROOT}/yaml

kubectl delete -f $YAML/broker.yaml
helm del --purge broker
make -C ${SCRIPTS_ROOT}/.. push
build_count=`cat ${SCRIPTS_ROOT}/.build`
echo $((build_count+1)) > ${SCRIPTS_ROOT}/.build
helm install $SCRIPTS_ROOT/../chart --name broker --namespace broker
$SCRIPTS_ROOT/update-broker-addr.sh
kubectl create -f $YAML/broker.yaml
