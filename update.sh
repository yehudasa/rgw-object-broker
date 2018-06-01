#!/bin/bash
kubectl delete -f broker.yaml
helm del --purge broker
make push
build_count=`cat .build`
echo $((build_count+1)) > .build
helm install chart --name broker --namespace broker
port=`kubectl get svc -n broker broker-rgw-object-broker-node-port | tail -1 | sed 's/.*://g' | sed 's/\/.*//g'`
cat broker.yaml.template | sed s/{port}/$port/g > broker.yaml
kubectl create -f broker.yaml
