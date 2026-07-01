import { createContext } from 'react';

// Markdown link handling. URLs (http/https/mailto) are confirmed via a modal then
// opened in a NEW window (#25). Path-like links open in the workspace editor via
// the existing code-ext bridge — dispatched as a window event FilesView listens
// for (#24). Provided per CurrentHistoryView instance so multi-card cards don't
// fight over one global handler.
export const OpenUrlContext = createContext<((url: string) => void) | null>(null);

// Question-bubble alignment. Default 'right' (chat style); the inline webframe
// history opts into 'left' via CurrentHistoryView's leftAlignQuestions prop.
// Context so we don't thread a prop through every CollapsibleQ call site.
export const QAlignContext = createContext<'left' | 'right'>('right');
