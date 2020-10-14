#!/usr/bin/python3
#
#   $ curl -s https://storage.googleapis.com/origin-ci-test/logs/release-openshift-origin-installer-e2e-aws-upgrade/1301652659597479936/artifacts/e2e-aws-upgrade/pods/openshift-cluster-version_cluster-version-operator-6b6bc46c59-dk8bm_cluster-version-operator.log | log-explainer.py
#   $ xdg-open index.html

import datetime
import logging
import re
import sys


# klog format parser
# https://github.com/kubernetes/klog/blob/v2.3.0/klog.go#L571-L585
log_regexp = re.compile('''^
    .*  # must-gather leading timestamp
    (?P<level>[IWEF])  # I(nfo), W(arning), E(rror), F(atal)
    (?P<month>[0-1][0-9])(?P<day>[0-3][0-9])[ ](?P<time>[0-9][0-9]:[0-9][0-9]:[0-9][0-9][.][0-9]+)  # timestamp
    [ ]+
    (?P<thread>[0-9]+)  # thread ID as returned by GetTID()
    [ ]
    (?P<file>[^:]*):(?P<line>[0-9]+)]
    [ ]
    (?P<message>.*)$
''', re.VERBOSE)


levels = {
    'I': 'Info',
    'W': 'Warning',
    'E': 'Error',
    'F': 'Fatal',
}


start_of_sync_cycle_regexp = re.compile('^Running sync (?P<version>[^ ]+) \(force=(?P<force>[^)]+)\) on generation (?P<generation>[0-9]+) in state (?P<state>[^ ]+) at attempt (?P<attempt>[0-9]+)$')
end_of_sync_cycle_prefix = 'Result of work: '
manifest_regexp = re.compile('^(?P<action>Running sync|Done syncing|error running apply) for (?P<type>[^ ]+) "(?P<name>[^"]+)" \((?P<index>[0-9]+) of (?P<total>[0-9]+)\)(?P<suffix>.*)$')


def read_klog(stream):
    lines = []
    line = None
    year = datetime.date.today().year  # assume the log is from this calendar year
    for line_string in sys.stdin.readlines():
        match = log_regexp.match(line_string)
        if not match:
            if line and line_string.strip():
                line['message'] += '\n' + line_string.strip()
            continue
        groups = match.groupdict()
        groups['year'] = year
        timestamp = datetime.datetime.strptime('{year} {month} {day} {time}'.format(**groups), '%Y %m %d %H:%M:%S.%f')
        line = {
            'level': levels[groups['level']],
            'time': timestamp,
            'thread': int(groups['thread']),
            'file': groups['file'],
            'line': int(groups['line']),
            'message': groups['message'],
        }
        lines.append(line)
    return lines


def parse_lines(lines):
    metadata = {
        'version': lines[0]['message'],
        'start_time': lines[0]['time'],
        'stop_time': lines[-1]['time'],
        'threads': len({line['thread'] for line in lines}),

        # goroutines
        'available updates sync': [line for line in lines if line['file'] == 'availableupdates.go' or 'syncing available updates' in line['message']],
        'cluster version sync': [line for line in lines if 'syncing cluster version' in line['message'] or 'Desired version from' in line['message']],
        'metrics': [line for line in lines if line['file'] == 'metrics.go'],
        'sync worker': [line for line in lines if line['file'] in ['sync_worker.go', 'task.go', 'task_graph.go', 'warnings.go']],
        'upgradeable': [line for line in lines if line['file'] == 'upgradeable.go' or 'syncing upgradeable' in line['message']],
    }

    metadata['leader_elections'] = [line for line in lines if line['file'] == 'leaderelection.go']
    acquired_leader_lease = [line for line in metadata['leader_elections'] if 'successfully acquired lease' in line['message']]
    if acquired_leader_lease:
        metadata['time_to_leader_lease'] = acquired_leader_lease[0]['time'] - metadata['start_time']

    sync = None
    metadata['syncs'] = []
    for line in metadata['sync worker']:
        match = start_of_sync_cycle_regexp.match(line['message'])
        if match:
            sync = match.groupdict()
            sync['start_time'] = line['time']
            sync['lines'] = []
            sync['manifests'] = {}
            metadata['syncs'].append(sync)
        if not sync:
            logging.warning('sync worker line outside of sync: {}'.format(line))
            continue
        sync['lines'].append(line)
        if line['message'].startswith(end_of_sync_cycle_prefix):
            sync['results'] = {
                'result': line['message'][len(end_of_sync_cycle_prefix):],
            }
            sync['results'].update(line)
        match = manifest_regexp.match(line['message'])
        if match:
            groups = match.groupdict()
            for key in ['index', 'total']:
                groups[key] = int(groups[key])
            groups.update(line)
            manifest_key = (groups['type'], groups['name'])
            if manifest_key not in sync['manifests']:
                sync['manifests'][manifest_key] = []
            sync['manifests'][manifest_key].append(groups)
    for sync in metadata['syncs']:
        sync['failed_manifests'] = {k: m for (k, m) in sync['manifests'].items() if m[-1]['action'] == 'error running apply'}

    metadata['unclassified'] = [
        line for line in lines
        if 'ClusterVersionOperator' not in line['message'] and
        line not in metadata['available updates sync'] and
        line not in metadata['cluster version sync'] and
        line not in metadata['metrics'] and
        line not in metadata['sync worker'] and
        line not in metadata['upgradeable'] and
        line not in metadata['leader_elections']
    ]
    return metadata


def write_sync_svg(path, metadata):
    time_ranges = {}
    for sync in metadata['syncs']:
        for key, manifest in sync['manifests'].items():
            start = (manifest[0]['time'] - metadata['start_time']).total_seconds()
            if 'Done syncing' in manifest[-1]['action']:
                stop = (manifest[-1]['time'] - metadata['start_time']).total_seconds()
            else:
                stop = None
            if key in time_ranges:
                previous = time_ranges[key]
                if previous[1]:
                    stop = previous[1]
                time_ranges[key] = (previous[0], stop, previous[2], previous[3], manifest)
            else:
                time_ranges[key] = (start, stop, manifest[0]['type'], manifest[0]['name'], manifest)

    for _, stop, objType, name, manifest in sorted(time_ranges.values()):
        if not stop:
            logging.warning('not finished: {} {}{}'.format(objType, name, manifest[-1]['suffix']))

    rectangles = []
    y = 0
    step = 4
    last_stop = 0
    max_stop = (metadata['stop_time'] - metadata['start_time']).total_seconds()
    for start, stop, objType, name, manifest in sorted(time_ranges.values()):
        if stop is None:
            stop = max_stop
            fill = 'red'
        else:
            fill = 'blue'
        rectangles.append('<rect x="{}" y="{}" width="{}" height="{}" fill="{}"><title>{} {} {}/{} ({})</title></rect>'.format(start, y, stop - start, step, fill, objType, name, manifest[0]['index'], manifest[0]['total'], stop - start))
        y += step
        if stop > last_stop:
            last_stop = stop

    with open(path, 'w') as f:
        f.write('<svg viewBox="0 0 {} {}" xmlns="http://www.w3.org/2000/svg">\n'.format(last_stop, y))
        duration = datetime.timedelta(seconds=last_stop)
        time_scale = 5
        for i in range(1 + int(last_stop // (time_scale*60))):
            f.write('  <line x1="{x}"  x2="{x}" y1="0%" y2="100%" stroke="gray" style="stroke-opacity: 0.5;"><title>{minutes} minutes</title></line>\n'.format(
                x=i * time_scale * 60,
                minutes=i * time_scale))
        for rectangle in rectangles:
            f.write('  {}\n'.format(rectangle))
        f.write('  <text x="50%" y="20" text-anchor="middle">CVO manifests, duration {}</text>\n'.format(duration))
        f.write('</svg>\n')


def write_log_lines(stream, lines):
    # FIXME: write to separate files?
    if lines:
        stream.write('<pre>\n{}\n</pre>\n'.format(
            '\n'.join(
                '{} {} {}'.format(line['time'], line['file'], line['message']) for line in lines
            )
        ))
    else:
        stream.write('    <p>No log lines.</p>\n')


def write_index(path, metadata, sync_svg):
    with open(path, 'w') as f:
        f.write('''<!doctype html>
<html lang="en">
<head>
  <title>Cluster-version operator log</title>
  <style type="text/css">
    .warning {
      font-weight: bold;
      color: red;
    }
    pre {
        font-size: 80%;
    }
  </style>
</head>
<body>
  <h1>Cluster-version operator log</h1>
''')
        f.write('  <dl>\n')
        for key in [
                'version',
                'start_time',
                'stop_time',
                'threads',
                ]:
            f.write('    <dt>{}</dt>\n    <dd>{}</dd>\n'.format(key, metadata.get(key, '-')))
        f.write('  </dl>\n')

        f.write('  <h2>Leader election</h2>\n')
        if metadata.get('time_to_leader_lease'):
            warning = ''
            if metadata['time_to_leader_lease'].total_seconds() > 30:
                warning = ' class="warning"'
            f.write('  <p{}>Leader lease acquired in {}.</p>'.format(warning, metadata['time_to_leader_lease']))
        else:
            f.write('  <p class="warning">Leader lease never acquired.</p>')
        write_log_lines(stream=f, lines=metadata.get('leader_elections'))

        f.write('  <h2>Peripheral goroutines</h2>\n')
        for key in [
                'available updates sync',
                'cluster version sync',
                'metrics',
                'upgradeable',
                ]:
            f.write('   <h3 id="{0}">{0}</h3>\n'.format(key))
            gorountine_lines = metadata.get(key)
            if gorountine_lines:
                time_to_start = gorountine_lines[0]['time'] - metadata['start_time']
                time_since_final_log = metadata['stop_time'] - gorountine_lines[-1]['time']
                warning = ''
                if (
                        time_to_start.total_seconds() > 2*60 or
                        (time_since_final_log.total_seconds() > 5*60 and key != 'metrics')  # metrics server does not log periodically
                        ):
                    warning = ' class="warning"'
                f.write('  <p{}>Goroutine beings logging {} after the initial container log entry and is last seen {} before the final container log entry.</p>\n'.format(
                    warning, time_to_start, time_since_final_log,
                ))

        if metadata.get('unclassified'):
            f.write('  <h2>Unclassified log lines</h2>\n')
            write_log_lines(stream=f, lines=metadata['unclassified'])

        f.write('  <h2 id="sync-cycles">Sync cycles</h2>\n')
        version = None
        mode = None
        for sync in metadata['syncs']:
            if sync['version'] != version:
                f.write('  <h3>{}</h3>\n'.format(sync['version']))
            version = sync['version']
            if (sync['state'], sync['force']) != mode:
                f.write('  <h4>{} (force={})</h4>\n'.format(sync['state'], sync['force']))
            mode = (sync['state'], sync['force'])
            end = 'running'
            if 'results' in sync:
                end = sync['results']['time']
            result = 'running'
            if 'results' in sync:
                if sync['results']['result'] == '[]':
                    result = 'success'
                else:
                    result = 'failure'
            f.write('  <h5>Attempt {} {}: {} - {}</h5>\n'.format(sync['attempt'], result, sync['start_time'], end))
            f.write('  <dl>\n')
            for (key, value) in [
                    ('lines', len(sync['lines'])),
                    ('result', sync.get('results', {}).get('result', 'running')),
                    ('manifests attempted', len(sync['manifests'])),
                    ]:
                f.write('    <dt>{}</dt>\n    <dd>{}</dd>\n'.format(key, value))
            f.write('  </dl>\n')
            if sync['failed_manifests']:
                f.write('  <p>Failed manifests:</p>\n')
                f.write('  <ul>\n')
                for lines in sorted(sync['failed_manifests'].values(), key=lambda lines: lines[-1]['index']):
                    f.write('    <li>{time} {message}</li>\n'.format(**lines[-1]))
                f.write('  </ul>\n')

        if metadata['sync worker']:
            time_since_final_log = metadata['stop_time'] - metadata['sync worker'][-1]['time']
            if time_since_final_log.total_seconds() > 5*60:
                f.write('  <p class="warning">Goroutine is last seen {} before the final container log entry.</p>\n'.format(time_since_final_log))

        f.write('  <div style="text-align:center">\n    <object data="{}" type="image/svg+xml" width="100%" />\n  </div>\n'.format(sync_svg))

        f.write('</body>\n</html>\n')


if __name__ == '__main__':
    lines = read_klog(stream=sys.stdin)
    metadata = parse_lines(lines=lines)
    write_sync_svg(path='sync.svg', metadata=metadata)
    write_index(path='index.html', metadata=metadata, sync_svg='sync.svg')
