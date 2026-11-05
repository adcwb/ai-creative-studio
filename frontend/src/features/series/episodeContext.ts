import {createContext,useContext} from 'react'
import type {EpisodeContext} from './types'
export const CurrentSeriesEpisode=createContext<EpisodeContext|null>(null)
export function useSeriesEpisode(){return useContext(CurrentSeriesEpisode)}
