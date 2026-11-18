import { mkdtemp, mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { BenchError } from './model';
import { home, tmp, type Binary } from './runtime';

export const cache = join(home, 'work/cache/sous-bench');
export function git(args: string[]) {
  const r = Bun.spawnSync(['git', ...args], { cwd: home, stdout: 'pipe', stderr: 'pipe' });
  if (r.exitCode) throw new BenchError('git-command-failed');
  return r.stdout.toString().trim();
}
export async function hashFile(path: string) {
  const cor = new Bun.CryptoHasher('sha256');
  for await (const chunk of Bun.file(path).stream()) cor.update(chunk);
  return cor.digest('hex');
}
const versionPattern = /^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,130}$/;
export async function build(ref: string, cancel: AbortSignal): Promise<Binary> {
  if (!versionPattern.test(ref) || ref.includes('..')) throw new BenchError('invalid-build-ref');
  const commit = git(['rev-parse', '--verify', '--end-of-options', `${ref}^{commit}`]);
  const tag = /^v\d+[.]/.test(ref) ? ref : `bench-${commit.slice(0, 12)}`;
  const root = join(cache, 'bin', commit, process.platform + '-' + process.arch);
  const suffix = process.platform === 'win32' ? '.exe' : '';
  const hub = join(root, 'sous-hub' + suffix), node = join(root, 'sous' + suffix);
  const stamp = join(root, 'build.json');
  const old = await Bun.file(stamp).json().catch(() => null);
  if (old?.label === tag && old?.commit === commit && old?.shaHub === await hashFile(hub).catch(() => '') && old?.shaNode === await hashFile(node).catch(() => '')) return { ...old, hub, node };
  await mkdir(root, { recursive: true });
  await mkdir(join(cache, 'go-build'), { recursive: true });
  const scratch = await mkdtemp(join(tmp, 'sous-bench-build-'));
  try {
    const tar = join(scratch, 'archive.tar');
    const archive = Bun.spawn(['git', 'archive', '--format=tar', `--output=${tar}`, commit, 'apps/core'], { cwd: home, signal: cancel, stdout: 'ignore', stderr: 'inherit' });
    if (await archive.exited) throw new BenchError('git-archive-failed');
    const expand = Bun.spawn(['tar', '-xf', tar, '-C', scratch], { cwd: scratch, signal: cancel, stderr: 'inherit' });
    if (await expand.exited) throw new BenchError('git-archive-extract-failed');
    const build = Bun.spawn(['go', 'build', '-trimpath', '-ldflags', `-s -w -X github.com/XiaoXianThis/Sous/apps/core/internal/version.Version=${tag}`, '-o', scratch + '/', './cmd/sous', './cmd/sous-hub'], {
      cwd: join(scratch, 'apps/core'), signal: cancel, env: { ...process.env, CGO_ENABLED: '0', GOCACHE: join(cache, 'go-build') }, stdout: 'inherit', stderr: 'inherit',
    });
    if (await build.exited) throw new BenchError('go-build-failed');
    await rename(join(scratch, 'sous-hub' + suffix), hub);
    await rename(join(scratch, 'sous' + suffix), node);
    const out = { label: tag, commit, shaHub: await hashFile(hub), shaNode: await hashFile(node) };
    await writeFile(stamp, JSON.stringify(out) + '\n');
    return { ...out, hub, node };
  } finally { await rm(scratch, { recursive: true, force: true }); }
}
