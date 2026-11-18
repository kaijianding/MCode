import type { RemoteProjectSummary } from '../types/contract';

export function chatsProject<T extends { kind?: RemoteProjectSummary['kind'] }>(
  projects: readonly T[]
): T | undefined {
  return projects.find((project) => project.kind === 'chats');
}

export function moveTargetProjects<T extends { kind?: RemoteProjectSummary['kind'] }>(
  projects: readonly T[]
): T[] {
  return projects.filter((project) => !project.kind);
}

export function pickableProjects<T extends { kind?: RemoteProjectSummary['kind'] }>(
  projects: readonly T[]
): T[] {
  const chats = chatsProject(projects);
  const regular = moveTargetProjects(projects);
  return chats ? [chats, ...regular] : regular;
}
