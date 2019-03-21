#!/usr/bin/python3
#
#   $ curl -s https://storage.googleapis.com/origin-ci-test/logs/release-openshift-origin-installer-e2e-aws-4.0/6016/artifacts/e2e-aws/pods/openshift-cluster-version_cluster-version-operator-74d8d99566-2bh4q_cluster-version-operator.log.gz | gunzip | cvo-waterfall.py >cvo.svg 

import datetime
import re
import sys


log_regexp = re.compile('^I[0-9]+ ([0-9:.]+) .* (Running sync|Done syncing) for ([^ ]+) "([^"]+)" \(([0-9]+) of ([0-9]+)\)')

resources = {}
reference_time = None
for line in sys.stdin.readlines():
    match = log_regexp.match(line)
    if not match:
        continue
    time, action, objType, name, index, total = match.groups()
    timestamp = datetime.datetime.strptime(time, '%H:%M:%S.%f')
    if reference_time is None:
        reference_time = timestamp
    if objType not in resources:
        resources[objType] = {}
    if name not in resources[objType]:
        resources[objType][name] = {
            'index': int(index),
            'total': int(total),
        }
    if action not in resources[objType][name]:
        resources[objType][name][action] = timestamp

time_ranges = []
for objType, names in resources.items():
    for name, data in names.items():
        if 'Done syncing' not in data:
            continue
        start = (data['Running sync'] - reference_time).total_seconds()
        stop = (data['Done syncing'] - reference_time).total_seconds()
        time_ranges.append((start, stop, objType, name, data))

rectangles = []
y = 0
step = 4
last_stop = 0
for start, stop, objType, name, data in sorted(time_ranges):
    rectangles.append('<rect x="{}" y="{}" width="{}" height="{}" fill="blue"><title>{} {} {}/{} ({})</title></rect>'.format(start, y, stop - start, step, objType, name, data['index'], data['total'], data['Done syncing'] - data['Running sync']))
    y += step
    if stop > last_stop:
        last_stop = stop

print('<svg viewBox="0 0 {} {}" xmlns="http://www.w3.org/2000/svg">'.format(last_stop, y))
for rectangle in rectangles:
    print('  {}'.format(rectangle))
print('</svg>')
