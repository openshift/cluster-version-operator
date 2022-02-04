#!/bin/bash
file="$1"

if test 1 -ne $#
then
  echo "Usage: $0 TARGET_FILE_NAME" >&2
  exit 1
fi

oc get $(oc api-resources --verbs=list -o name | awk '{printf "%s%s",sep,$0;sep=","}')  --ignore-not-found --all-namespaces -o=custom-columns=APIVERSION:apiVersion,KIND:.kind,NAME:.metadata.name,NAMESPACE:.metadata.namespace --sort-by='apiVersion' > ${file} 
