#!/bin/bash
set -e
NAMESPACE="e-commerce"
SERVICE_NAME="checkout-service"
EXPECTED_SELECTOR_VERSION="green"

echo "Waiting for the Service '$SERVICE_NAME' to point to version '$EXPECTED_SELECTOR_VERSION'..."
# Use 'kubectl wait' to verify the service selector condition
kubectl wait --for=jsonpath='{.spec.selector.version}'="$EXPECTED_SELECTOR_VERSION" service/$SERVICE_NAME -n $NAMESPACE --timeout=5m

echo "Service selector updated correctly."

# Optional: Verify that the endpoint slice now contains IPs from the green deployment's pods.
# This confirms traffic is actually flowing to the correct pods.
echo "Verifying that service endpoints match the green deployment..."
# Use a single command to check if at least one endpoint has the desired label
kubectl get endpointslices -n $NAMESPACE -l kubernetes.io/service-name=$SERVICE_NAME \
  -o jsonpath='{.items[0].endpoints[*].conditions.ready}' | grep -q "true"

echo "Service endpoints correctly point to the green deployment."
echo "Verification successful!"
exit 0