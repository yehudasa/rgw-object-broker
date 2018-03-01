#!/bin/bash
count=`cat .count`
count=$((count+1))
echo $count > .count
cat instance.yaml.template | sed s/{count}/$count/g > instance.yaml
kubectl --context=service-catalog create -f instance.yaml 
