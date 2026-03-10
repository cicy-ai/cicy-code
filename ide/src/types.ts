export interface Position {
  x: number;
  y: number;
}

export interface Size {
  width: number;
  height: number;
}

export interface AppSettings {
  panelPosition: Position;
  panelSize: Size;
  forwardEvents: boolean;
  lastDraft?: string;
  showPrompt: boolean;
  showVoiceControl: boolean;
  voiceButtonPosition: Position;
  commandHistory: string[];
  agent_duty?: string;
}
