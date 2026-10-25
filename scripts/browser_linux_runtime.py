"""Admission checks shared by the two private Linux browser entry points."""
import importlib.util
import os
from pathlib import Path
import socket
import sys

from validation_workspace import ROOT

_spec = importlib.util.spec_from_file_location('pm2_namespace_policy', Path(__file__).with_name('pm2-real-validation.py'))
namespace = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(namespace)


def admitted_runtime():
    if sys.platform != 'linux' or not namespace.namespace_supervisor():
        return False
    status = dict(line.split(':', 1) for line in Path('/proc/self/status').read_text().splitlines() if ':' in line)
    return (os.geteuid() != 0 and not os.getgroups() and status.get('NoNewPrivs', '').strip() == '1'
            and not any(int(status[key].strip(), 16) for key in ('CapEff', 'CapPrm', 'CapInh', 'CapBnd', 'CapAmb'))
            and {name for _, name in socket.if_nameindex()} == {'lo'}
            and len(Path('/proc/net/route').read_text().splitlines()) == 1
            and (ROOT / '.canary/bounded-validation.lock').is_file())
