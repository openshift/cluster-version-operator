#!/usr/bin/env python

import os.path


def generate_lib_resources(directory, types, clients, modifiers, health_checks):
    generate_resourceread(directory=os.path.join(directory, 'resourceread'), types=types)
    generate_resourcebuilder(directory=os.path.join(directory, 'resourcebuilder'), types=types, clients=clients, modifiers=modifiers, health_checks=health_checks)


def generate_resourceread(directory, types):
    package_name = os.path.basename(directory)
    path = os.path.join(directory, package_name + '.go')
    lines = [
        '// auto-generated with generate-lib-resources.py',
        '',
        '// Package {} reads supported objects from bytes.'.format(package_name),
        'package {}'.format(package_name),
        '',
        'import (',
    ]

    imports = {}
    for import_name in [
            'k8s.io/apimachinery/pkg/runtime',
            'k8s.io/apimachinery/pkg/runtime/serializer',
            ]:
        imports[import_name] = '\t"{}"'.format(import_name)

    for package in sorted(types.keys()):
        base, version = os.path.split(package)  # FIXME: should be using network path operations on package names
        short_name = os.path.basename(base)
        imports[package] = '\t{}{} "{}"'.format(short_name, version, package)

    lines.extend([import_line for _, import_line in sorted(imports.items(), key=lambda package_line: package_line[0])])
    lines.extend([
        ')',
        '',
        'var (',
        '\tscheme  = runtime.NewScheme()',
        '\tcodecs  = serializer.NewCodecFactory(scheme)',
        '\tdecoder runtime.Decoder',
        ')',
        '',
        'func init() {',
    ])

    sgvs = scheme_group_versions(types=types)
    for _, data in sorted(sgvs.items()):
        lines.extend([
            '\tif err := {0}{1}.AddToScheme(scheme); err != nil {{'.format(data['short_name'], data['version']),
            '\t\tpanic(err)',
            '\t}',
        ])

    lines.append('\tdecoder = codecs.UniversalDecoder(')
    lines.extend(['\t\t{},'.format(sgv) for sgv in sorted(sgvs.keys())])
    lines.extend([
        '\t)',
        '}',
        '',
        '// Read reads an object from bytes.',
        'func Read(objBytes []byte) (runtime.Object, error) {',
        '\treturn runtime.Decode(decoder, objBytes)',
        '}',
        '',
        '// ReadOrDie reads an object from bytes.  Panics on error.',
        'func ReadOrDie(objBytes []byte) runtime.Object {',
        '\trequiredObj, err := runtime.Decode(decoder, objBytes)',
        '\tif err != nil {',
        '\t\tpanic(err)',
        '\t}',
        '\treturn requiredObj',
        '}',
        '',  # trailing newline
    ])
    with open(path, 'w') as f:
        f.write('\n'.join(lines))


def generate_resourcebuilder(directory, types, clients, modifiers, health_checks):
    package_name = os.path.basename(directory)
    path = os.path.join(directory, package_name + '.go')
    lines = [
        '// auto-generated with generate-lib-resources.py',
        '',
        '// Package {} reads supported objects from bytes.'.format(package_name),
        'package {}'.format(package_name),
        '',
        'import (',
        '\t"context"',
        '\t"fmt"',
        '',
    ]

    imports = {}
    for import_name in [
            'github.com/openshift/cluster-version-operator/lib',
            'github.com/openshift/cluster-version-operator/lib/resourceapply',
            'github.com/openshift/cluster-version-operator/lib/resourceread',
            'k8s.io/client-go/rest',
            ]:
        imports[import_name] = '\t"{}"'.format(import_name)

    ignored_packages = set()

    for package in types.keys():
        if not clients.get(package):
            ignored_packages.add(package)
            continue
        base, version = os.path.split(package)
        short_name = os.path.basename(base)
        imports[package] = '\t{}{} "{}"'.format(short_name, version, package)

    client_properties = {}
    for package, client in clients.items():
        if package in ignored_packages:
            continue
        base, version = os.path.split(client['package'])
        short_name = os.path.basename(base)
        client_short_name = '{}client{}'.format(short_name, version)
        imports[client['package']] = '\t{} "{}"'.format(client_short_name, client['package'])
        client_properties['{}Client{}'.format(short_name, version)] = {
            'package': package,
            'client_short_name': client_short_name,
            'type': '*{}.{}'.format(client_short_name, client['type']),
            'protobuf': client['package'].startswith('k8s.io/') and 'kube-aggregator' not in client['package'],
        }

    lines.extend([import_line for _, import_line in sorted(imports.items(), key=lambda package_line: package_line[0])])

    longest_property = max(len(prop_name) for prop_name in client_properties.keys())

    lines.extend([
        ')',
        '',
        '// builder manages single-manifest cluster reconciliation and monitoring.',
        'type builder struct {',
        '\traw      []byte',
        '\tmode     Mode',
        '\tmodifier MetaV1ObjectModifierFunc',
        '',
    ])
    lines.extend([
        '\t{:{width}} {}'.format(prop_name, data['type'], width=longest_property)
        for prop_name, data in sorted(client_properties.items())
    ])

    lines.extend([
        '}',
        '',
        'func newBuilder(config *rest.Config, m lib.Manifest) Interface {',
        '\treturn &builder{',
        '\t\traw: m.Raw,',
        '',
    ])
    for prop_name, data in sorted(client_properties.items()):
        new_client_arg = 'config'
        if data.get('protobuf'):
            new_client_arg = 'withProtobuf({})'.format(new_client_arg)
        lines.append('\t\t{:{width}} {}.NewForConfigOrDie({}),'.format(prop_name + ':', data['client_short_name'], new_client_arg, width=longest_property+1))

    lines.extend([
        '\t}',
        '}',
        '',
        'func (b *builder) WithMode(m Mode) Interface {',
        '\tb.mode = m',
        '\treturn b',
        '}',
        '',
        'func (b *builder) WithModifier(f MetaV1ObjectModifierFunc) Interface {',
        '\tb.modifier = f',
        '\treturn b',
        '}',
        '',
        'func (b *builder) Do(ctx context.Context) error {',
        '\tobj := resourceread.ReadOrDie(b.raw)',
        '',
        '\tswitch typedObject := obj.(type) {'
    ])

    for package, type_names in sorted(types.items()):
        if package in ignored_packages:
            continue
        base, version = os.path.split(package)
        short_name = os.path.basename(base)
        try:
            client_prop_name = [key for key, data in client_properties.items() if data['package'] == package][0]
        except IndexError as error:
            raise ValueError('no client property found for {}'.format(package))
        for type_name in sorted(type_names):
            lines.extend([
                '\tcase *{}{}.{}:'.format(short_name, version, type_name),
                '\t\tif b.modifier != nil {',
                '\t\t\tb.modifier(typedObject)',
                '\t\t}',
            ])
            type_key = (package, type_name)
            modifier = modifiers.get(type_key)
            if modifier:
                lines.extend([
                    '\t\tif err := {}(ctx, typedObject); err != nil {{'.format(modifier),
                    '\t\t\treturn err',
                    '\t\t}',
                ])
            lines.extend([
                '\t\tif _, _, err := resourceapply.Apply{}{}(ctx, b.{}, typedObject); err != nil {{'.format(type_name, version, client_prop_name),
                '\t\t\treturn err',
                '\t\t}',
            ])
            health_check = health_checks.get(type_key)
            if health_check:
                lines.append('\t\treturn {}(ctx, typedObject)'.format(health_check))

    lines.extend([
        '\tdefault:',
        '\t\treturn fmt.Errorf("unrecognized manifest type: %T", obj)',
        '\t}',
        '',
        '\treturn nil',
        '}',
        '',
        'func init() {',
        '\trm := NewResourceMapper()',
    ])

    for sgv, data in sorted(scheme_group_versions(types=types).items()):
        if data['package'] in ignored_packages:
            continue
        for type_name in sorted(data['types']):
            lines.append('\trm.RegisterGVK({}.WithKind("{}"), newBuilder)'.format(sgv, type_name))

    lines.extend([
        '\trm.AddToMap(Mapper)',
        '}',
        '',  # trailing newline
    ])

    with open(path, 'w') as f:
        f.write('\n'.join(lines))


def scheme_group_versions(types):
    sgvs = {}
    for package, type_names in types.items():
        base, version = os.path.split(package)
        short_name = os.path.basename(base)
        sgv = '{}{}.SchemeGroupVersion'.format(short_name, version)
        sgvs[sgv] = {
            'package': package,
            'short_name': short_name,
            'types': type_names,
            'version': version,
        }
    return sgvs


if __name__ == '__main__':
    types = {
        'github.com/openshift/api/image/v1': {'ImageStream'}, # for payload loading
        'github.com/openshift/api/security/v1': {'SecurityContextConstraints'},
        'k8s.io/api/apps/v1': {'DaemonSet', 'Deployment'},
        'k8s.io/api/batch/v1': {'Job'},
        'k8s.io/api/core/v1': {'ConfigMap', 'Namespace', 'Service', 'ServiceAccount'},
        'k8s.io/api/rbac/v1': {'ClusterRole', 'ClusterRoleBinding', 'Role', 'RoleBinding'},
        'k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1': {'CustomResourceDefinition'},
        'k8s.io/kube-aggregator/pkg/apis/apiregistration/v1': {'APIService'},
    }

    clients = {
        'github.com/openshift/api/security/v1': {'package': 'github.com/openshift/client-go/security/clientset/versioned/typed/security/v1', 'type': 'SecurityV1Client'},
        'github.com/openshift/api/config/v1': {'package': 'github.com/openshift/client-go/config/clientset/versioned/typed/config/v1', 'type': 'ConfigV1Client'},
        'k8s.io/api/apps/v1': {'package': 'k8s.io/client-go/kubernetes/typed/apps/v1', 'type': 'AppsV1Client'},
        'k8s.io/api/batch/v1': {'package': 'k8s.io/client-go/kubernetes/typed/batch/v1', 'type': 'BatchV1Client'},
        'k8s.io/api/core/v1': {'package': 'k8s.io/client-go/kubernetes/typed/core/v1', 'type': 'CoreV1Client'},
        'k8s.io/api/rbac/v1': {'package': 'k8s.io/client-go/kubernetes/typed/rbac/v1', 'type': 'RbacV1Client'},
        'k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1': {'package': 'k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1', 'type': 'ApiextensionsV1Client'},
        'k8s.io/kube-aggregator/pkg/apis/apiregistration/v1': {'package': 'k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1', 'type': 'ApiregistrationV1Client'},
    }

    modifiers = {
        ('k8s.io/api/apps/v1', 'Deployment'): 'b.modifyDeployment',
        ('k8s.io/api/apps/v1', 'DaemonSet'): 'b.modifyDaemonSet',
    }

    health_checks = {
        ('k8s.io/api/apps/v1', 'Deployment'): 'b.checkDeploymentHealth',
        ('k8s.io/api/apps/v1', 'DaemonSet'): 'b.checkDaemonSetHealth',
        ('k8s.io/api/batch/v1', 'Job'): 'b.checkJobHealth',
    }

    generate_lib_resources(directory='lib', types=types, clients=clients, modifiers=modifiers, health_checks=health_checks)
