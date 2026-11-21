import { createContext, useContext } from 'react'
import type { SupervisorBinding, WorkspaceCapabilities } from './types'

/** Null in relay mode; direct workspaces advertise their actual capabilities. */
export const DirectWorkspaceContext = createContext<WorkspaceCapabilities | null>(null)
export const useDirectWorkspace = () => useContext(DirectWorkspaceContext)

export const SupervisorBindingContext = createContext<(binding: SupervisorBinding) => void>(() => {})
