# Elastic Agent Enrollment — Both VMs

The infrastructure policy in `agent.yml` (System integration, syslog, network/firewall
flow logs) must be enrolled on **both** Ubuntu VMs: the **bastion host** and the
**CI runner**. Enroll each host into the same Fleet policy so they share integration
config but report as distinct hosts in `Kibana → Observability → Infrastructure → Inventory`.

## 1. Create / import the Fleet policy

Import the policy definition into Fleet (Kibana → Fleet → Agent policies → Upload),
or apply `agent.yml` as a standalone agent config. The policy id is
`sre-assessment-infrastructure-policy`.

## 2. Obtain an enrollment token

Kibana → Fleet → Enrollment tokens → create/select the token bound to
`sre-assessment-infrastructure-policy`. Export it and the Fleet Server URL:

```bash
export FLEET_URL="https://fleet-server.elastic-system.svc.cluster.local:8220"
export ENROLLMENT_TOKEN="<token-from-fleet>"
export AGENT_VERSION="8.12.0"
```

## 3. Install the agent on EACH VM

Run the same block on **bastion** and again on **ci-runner** (SSH into each host):

```bash
curl -L -O https://artifacts.elastic.co/downloads/beats/elastic-agent/elastic-agent-${AGENT_VERSION}-linux-x86_64.tar.gz
tar xzvf elastic-agent-${AGENT_VERSION}-linux-x86_64.tar.gz
cd elastic-agent-${AGENT_VERSION}-linux-x86_64

sudo ./elastic-agent install \
  --url="${FLEET_URL}" \
  --enrollment-token="${ENROLLMENT_TOKEN}" \
  --tag="role:bastion,env:assessment"        # use role:ci-runner on the CI runner
```

> Set a distinct `--tag` per host (`role:bastion` vs `role:ci-runner`) so hosts are
> filterable in the Infrastructure Inventory and in the VM alert rules.

## 4. Grant log read access

The `filestream` inputs read `/var/log/syslog`, `/var/log/auth.log`,
`/var/log/calico/flowlogs/flows.log`, and `/var/log/kubernetes/audit/audit.log`.
The agent runs as root via `elastic-agent install`, which covers these paths. On the
CI runner (no Calico/kube-audit logs), those inputs simply produce no data — that is
expected and harmless.

## 5. Verify

- Fleet → Agents: both hosts show **Healthy**.
- Observability → Infrastructure → Inventory: two hosts visible with live CPU/mem.
- Observability → Infrastructure → Host Details: open each host and confirm the
  CPU / memory / disk I/O / network charts are populated.

The three VM alert rules (`SRE - High CPU Sustained`, `SRE - Disk Space Critical`,
`SRE - Memory Pressure`) apply to both hosts because they filter on
`labels.environment: assessment`, which both agents attach.
