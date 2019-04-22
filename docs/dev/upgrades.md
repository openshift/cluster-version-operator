# Upgrades and order

In the CVO, upgrades are performed in the order described in the payload (lexographic), with operators only running in parallel if they share the same file prefix `0000_NN_` and differ by the next chunk `OPERATOR`, i.e. `0000_70_authentication-operator_*` and `0000_70_image-registry-operator_*` are run in parallel.

Priorities during upgrade:

1. Simplify the problems that can occur
2. Ensure applications aren't disrupted during upgrade
3. Complete within a reasonable time period (30m-1h for control plane)

During upgrade we bias towards predictable ordering for operators that lack sophistication about detecting their prerequisites. Over time, operators should be better at detecting their prerequisites without overcomplicating or risking the predictability of upgrades.

Currently, upgrades proceed in operator order without distinguishing between node and control plane components. Future improvements may allow nodes to upgrade independently and at different schedules to reduce production impact. This in turn complicates the investment operator teams must make in testing and understanding how to version their control plane components independently of node infrastructure.

All components must be N-1 minor version (4.y and 4.y-1) compatible - a component must update its operator first, then its dependents.  All operators and control plane components MUST handle running with their dependents at a N-1 minor version for extended periods and test in that scenario.

## Generalized ordering

The following rough order is defined for how upgrades should proceed:

```
config-operator

kube-apiserver

kcm/ksched

important, internal apis, likely to break unless tested:
* cloud-credential-operator
* openshift-apiserver

non-disruptive:
* everything

olm (maybe later move to post disruptive)

maximum disruptive:
  node-specific daemonsets are on-disruptive:
  * network
  * dns

  eventually push button separate node upgrade:
  * mco, mao, cloud-operators
```

Which in practice can be described in runlevels:

```
0000_10_*: config-operator
0000_20_*: kube-apiserver
0000_25_*: kube scheduler and controller manager
0000_30_*: other apiservers: openshift and machine
0000_40_*: reserved
0000_50_*: all non-order specific components
0000_60_*: reserved
0000_70_*: disruptive node-level components: dns, sdn, multus
0000_80_*: machine operators
0000_90_*: reserved for any post-machine updates
```