import type { RemoteProjectSummary } from '../types/contract';

export function chatsProject<T extends { kind?: RemoteProjectSummary['kind'] }>(
  projects: readonly T[]
): T | undefined {
  return projects.find((project) => project.kind === 'chats');
}

export function pickableProjects<T extends { kind?: RemoteProjectSummary['kind'] }>(
  projects: readonly T[]
): T[] {
  const chats = chatsProject(projects);
  const regular = projects.filter((project) => !project.kind);
  return chats ? [chats, ...regular] : regular;
}
