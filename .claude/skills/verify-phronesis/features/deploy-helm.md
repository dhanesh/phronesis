# deploy-helm: Install the Helm chart with Dex SSO on Kubernetes

- id: deploy-helm
- proven: no
- anchors: deploy/helm/phronesis/templates/deployment.yaml, deploy/helm/phronesis/values.yaml, deploy/helm/smoke.sh, deploy/helm/test/dex.yaml, deploy/smoke/smoke.mjs
- constraints:

## What it is

The Helm chart installs phronesis (one replica, Recreate, PVC, non-root, read-only root
filesystem, probes, required secrets) next to Dex in a namespace on Colima k3s. The same
browser smoke runs through port-forwards; then the pod is deleted and the replacement
pod serves the same data from the PVC.

## How to reach it

- k3s: Colima profile `phronesis-k8s` (`colima start phronesis-k8s --kubernetes`),
  kube context `colima-phronesis-k8s`.
- Image: `make image DOCKER_CONTEXT=colima-phronesis-k8s` (k3s uses that Docker).
- Namespace = instance; `helm upgrade --install phronesis deploy/helm/phronesis`.

## Drive it

```sh
make image DOCKER_CONTEXT=colima-phronesis-k8s
"$SKILL/scripts/container.sh" deploy-helm "$INSTANCE_HELM"
```

Expected: exit code 0 and JSON with `"ok": true`. Checks: the same `full:` and `persist:`
browser checks as deploy-compose, then smoke_script_pass, image_revision_is_head,
pod_replaced_on_same_pvc.
Exit 1 means a check failed; exit 2 means the image is not HEAD or ports could not be claimed.

## Proof

`full.json` and `persist.json` hold every browser check; `smoke.log` shows the rollout,
`pod replaced: <old> -> <new>` and `HELM_SMOKE: PASS`.
Side effect to record: PVC phronesis-data kept the page, role and key across a pod deletion.

## Gotchas

- `colima start` switches the global docker and kubectl contexts; the scripts always pass
  `--context`, and you should switch the user's contexts back after starting a profile.
- The smoke deletes only its own namespace.
