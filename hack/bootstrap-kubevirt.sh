#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo 'usage: hack/bootstrap-kubevirt.sh KUBECONFIG CONTEXT' >&2
  exit 2
fi
kubeconfig="$1"
context="$2"
kubevirt_version=v1.8.4
cdi_version=v1.66.1
kubectl_args=(--kubeconfig "${kubeconfig}" --context "${context}")

kubectl "${kubectl_args[@]}" get nodes
kubectl "${kubectl_args[@]}" apply -f "https://github.com/kubevirt/kubevirt/releases/download/${kubevirt_version}/kubevirt-operator.yaml"
kubectl "${kubectl_args[@]}" apply -f "https://github.com/kubevirt/kubevirt/releases/download/${kubevirt_version}/kubevirt-cr.yaml"
kubectl "${kubectl_args[@]}" -n kubevirt wait kubevirt kubevirt --for=condition=Available --timeout=900s
kubectl "${kubectl_args[@]}" apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${cdi_version}/cdi-operator.yaml"
kubectl "${kubectl_args[@]}" apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${cdi_version}/cdi-cr.yaml"
kubectl "${kubectl_args[@]}" wait cdi cdi --for=condition=Available --timeout=600s
