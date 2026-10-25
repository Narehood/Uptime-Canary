"""Install the optional service's separate locked tree through the storage guard."""
import json
import os
import shutil
import subprocess

from validation_workspace import ROOT, guarded_output


def main():
    output = guarded_output('game-dependencies')
    for name in ('user.conf', 'global.conf'):
        (output / name).write_text('')
    env = {k: v for k, v in os.environ.items() if k.upper() not in ('NODE_OPTIONS', 'NODE_PATH')
           and not k.upper().startswith(('CANARY_', 'NPM_'))}
    env.update(npm_config_cache=str(output / 'cache'), npm_config_userconfig=str(output / 'user.conf'),
               npm_config_globalconfig=str(output / 'global.conf'), npm_config_registry='https://registry.npmjs.org',
               npm_config_update_notifier='false')
    lock = json.loads((ROOT / 'services/game-query/package-lock.json').read_text())
    if lock['packages']['node_modules/gamedig']['version'] != '5.3.3':
        raise ValueError('Unexpected GameDig dependency profile')
    command = [shutil.which('npm.cmd' if os.name == 'nt' else 'npm'), 'ci', '--prefix', str(ROOT / 'services/game-query'),
               '--ignore-scripts', '--no-audit', '--no-fund', '--no-bin-links', '--workspaces=false', '--omit=optional']
    subprocess.run(command, env=env, cwd=ROOT, check=True, timeout=120)


if __name__ == '__main__':
    main()
