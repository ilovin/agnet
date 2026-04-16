#!/bin/bash
# Create K8s secret for TunnelHub multi-user config
kubectl create secret generic phonetalk-tunnelhub-secret \
  --from-literal=users="yehong.yang:yehong.yang;fengming.xie:fengming.xie" \
  --dry-run=client -o yaml | kubectl apply -f -
