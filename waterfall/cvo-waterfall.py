#!/usr/bin/python3
#
#   $ curl -s https://storage.googleapis.com/origin-ci-test/logs/release-openshift-origin-installer-e2e-aws-4.0/6016/artifacts/e2e-aws/pods/openshift-cluster-version_cluster-version-operator-74d8d99566-2bh4q_cluster-version-operator.log.gz | gunzip | cvo-waterfall.py >cvo.svg 

import datetime
import logging
import re
import sys


log_regexp = re.compile('^I[0-9]+ ([0-9:.]+) .* (Running sync|Done syncing) for ([^ ]+) "([^"]+)" \(([0-9]+) of ([0-9]+)\)')

resources = {}
reference_time = last_log = None
for line in sys.stdin.readlines():
    match = log_regexp.match(line)
    if not match:
        continue
    time, action, objType, name, index, total = match.groups()
    timestamp = datetime.datetime.strptime(time, '%H:%M:%S.%f')
    if reference_time is None:
        reference_time = timestamp
    last_log = timestamp
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
        start = (data['Running sync'] - reference_time).total_seconds()
        if 'Done syncing' in data:
            stop = (data['Done syncing'] - reference_time).total_seconds()
        else:
            stop = None
            logging.warning('not finished: {} {}'.format(objType, name))
        time_ranges.append((start, stop, objType, name, data))

rectangles = []
y = 0
step = 4
last_stop = 0
max_stop = (last_log - reference_time).total_seconds()
for start, stop, objType, name, data in sorted(time_ranges):
    if stop is None:
        stop = max_stop
        data['Done syncing'] = last_log
        fill = 'red'
    else:
        fill = 'blue'
    rectangles.append('<rect x="{}" y="{}" width="{}" height="{}" fill="{}"><title>{} {} {}/{} ({})</title></rect>'.format(start, y, stop - start, step, fill, objType, name, data['index'], data['total'], data['Done syncing'] - data['Running sync']))
    y += step
    if stop > last_stop:
        last_stop = stop

print('<svg viewBox="0 0 {} {}" xmlns="http://www.w3.org/2000/svg">'.format(last_stop, y))
duration = datetime.timedelta(seconds=last_stop)
time_scale = 5
for i in range(1 + int(last_stop // (time_scale*60))):
    print('  <line x1="{x}"  x2="{x}" y1="0%" y2="100%" stroke="gray" style="stroke-opacity: 0.5;"><title>{minutes} minutes</title></line>'.format(
        x=i * time_scale * 60,
        minutes=i * time_scale))
for rectangle in rectangles:
    print('  {}'.format(rectangle))
print('  <text x="50%" y="20" text-anchor="middle">CVO manifests, duration {}</text>'.format(duration))
print('</svg>')
