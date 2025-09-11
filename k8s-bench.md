# K8s-bench Evaluation Results

## Model Performance Summary

| Model | Success | Fail |
|-------|---------|------|
| gemini-2.5-flash | 17 | 8 |
| gemini-2.5-pro | 23 | 2 |
| google/gemma-3-27b-it | 13 | 12 |
| **Total** | 53 | 22 |

## Overall Summary

- Total Runs: 75
- Overall Success: 53 (70%)
- Overall Fail: 22 (29%)

## Model: gemini-2.5-flash

| Task | Provider | Result |
|------|----------|--------|
| create-canary-deployment | gemini | ✅ success |
| create-network-policy | gemini | ❌ fail |
| create-pod | gemini | ✅ success |
| create-pod-mount-configmaps | gemini | ✅ success |
| create-pod-resources-limits | gemini | ✅ success |
| create-simple-rbac | gemini | ✅ success |
| debug-app-logs | gemini | ✅ success |
| deployment-traffic-switch | gemini | ✅ success |
| fix-crashloop | gemini | ❌ fail |
| fix-image-pull | gemini | ❌ fail |
| fix-oomkilled | gemini | ✅ success |
| fix-pending-pod | gemini | ✅ success |
| fix-probes | gemini | ❌ fail |
| fix-rbac-wrong-resource | gemini | ✅ success |
| fix-service-routing | gemini | ✅ success |
| fix-service-with-no-endpoints | gemini | ✅ success |
| horizontal-pod-autoscaler | gemini | ❌ fail |
| list-images-for-pods | gemini | ✅ success |
| multi-container-pod-communication | gemini | ✅ success |
| resize-pvc | gemini | ❌ fail |
| rolling-update-deployment | gemini | ❌ fail |
| scale-deployment | gemini | ✅ success |
| scale-down-deployment | gemini | ✅ success |
| setup-dev-cluster | gemini | ✅ success |
| statefulset-lifecycle | gemini | ❌ fail |

**gemini-2.5-flash Summary**

- Total: 25
- Success: 17 (68%)
- Fail: 8 (32%)

## Model: gemini-2.5-pro

| Task | Provider | Result |
|------|----------|--------|
| create-canary-deployment | gemini | ✅ success |
| create-network-policy | gemini | ❌ fail |
| create-pod | gemini | ✅ success |
| create-pod-mount-configmaps | gemini | ✅ success |
| create-pod-resources-limits | gemini | ✅ success |
| create-simple-rbac | gemini | ✅ success |
| debug-app-logs | gemini | ✅ success |
| deployment-traffic-switch | gemini | ✅ success |
| fix-crashloop | gemini | ✅ success |
| fix-image-pull | gemini | ✅ success |
| fix-oomkilled | gemini | ✅ success |
| fix-pending-pod | gemini | ✅ success |
| fix-probes | gemini | ✅ success |
| fix-rbac-wrong-resource | gemini | ✅ success |
| fix-service-routing | gemini | ✅ success |
| fix-service-with-no-endpoints | gemini | ✅ success |
| horizontal-pod-autoscaler | gemini | ❌ fail |
| list-images-for-pods | gemini | ✅ success |
| multi-container-pod-communication | gemini | ✅ success |
| resize-pvc | gemini | ✅ success |
| rolling-update-deployment | gemini | ✅ success |
| scale-deployment | gemini | ✅ success |
| scale-down-deployment | gemini | ✅ success |
| setup-dev-cluster | gemini | ✅ success |
| statefulset-lifecycle | gemini | ✅ success |

**gemini-2.5-pro Summary**

- Total: 25
- Success: 23 (92%)
- Fail: 2 (8%)

## Model: google/gemma-3-27b-it

| Task | Provider | Result |
|------|----------|--------|
| create-canary-deployment | openai | ❌ fail |
| create-network-policy | openai | ❌ fail |
| create-pod | openai | ✅ success |
| create-pod-mount-configmaps | openai | ❌ fail |
| create-pod-resources-limits | openai | ✅ success |
| create-simple-rbac | openai | ✅ success |
| debug-app-logs | openai | ✅ success |
| deployment-traffic-switch | openai | ✅ success |
| fix-crashloop | openai | ✅ success |
| fix-image-pull | openai | ✅ success |
| fix-oomkilled | openai | ✅ success |
| fix-pending-pod | openai | ✅ success |
| fix-probes | openai | ❌ fail |
| fix-rbac-wrong-resource | openai | ✅ success |
| fix-service-routing | openai | ❌ fail |
| fix-service-with-no-endpoints | openai | ❌ fail |
| horizontal-pod-autoscaler | openai | ❌ fail |
| list-images-for-pods | openai | ❌ fail |
| multi-container-pod-communication | openai | ✅ success |
| resize-pvc | openai | ❌ fail |
| rolling-update-deployment | openai | ❌ fail |
| scale-deployment | openai | ✅ success |
| scale-down-deployment | openai | ✅ success |
| setup-dev-cluster | openai | ❌ fail |
| statefulset-lifecycle | openai | ❌ fail |

**google/gemma-3-27b-it Summary**

- Total: 25
- Success: 13 (52%)
- Fail: 12 (48%)

---

_Report generated on September 11, 2025 at 1:02 PM_
