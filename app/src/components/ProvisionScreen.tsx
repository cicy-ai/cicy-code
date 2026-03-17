import { useState, useEffect, useRef } from 'react';
import { TokenManager } from '../services/tokenManager';
import config from '../config';

interface Step {
  step: number;
  total: number;
  status: string;
  message: string;
}

const LABELS = [
  'Creating Cloudflare Tunnel',
  'Creating server',
  'Uploading deploy script',
  'Deploying services',
  'Verifying',
];

export default function ProvisionScreen({ onReady }: { onReady: (backend: string) => void }) {
  const [steps, setSteps] = useState<Step[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [elapsed, setElapsed] = useState(0);
  const retryRef = useRef(0);
  const startRef = useRef(Date.now());
  const logEndRef = useRef<HTMLDivElement>(null);

  // Timer
  useEffect(() => {
    const t = setInterval(() => setElapsed(Math.floor((Date.now() - startRef.current) / 1000)), 1000);
    return () => clearInterval(t);
  }, []);

  // Auto-scroll logs
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [logs]);

  useEffect(() => {
    const token = TokenManager.getToken();
    if (!token) return;

    const connect = () => {
      const es = new EventSource(`${config.mgrBase}/api/provision/stream?token=${token}`);

      es.onmessage = (e) => {
        const data: Step = JSON.parse(e.data);

        setLogs(prev => [...prev, `[Step ${data.step}/${data.total}] ${data.status}: ${data.message}`]);

        if (data.status === 'done') {
          es.close();
          onReady(data.message);
          return;
        }

        if (data.status === 'error') {
          es.close();
          setError(data.message);
          return;
        }

        setSteps(prev => {
          const exists = prev.find(s => s.step === data.step);
          if (exists) return prev.map(s => s.step === data.step ? data : s);
          return [...prev, data];
        });
      };

      es.onerror = () => {
        es.close();
        setLogs(prev => [...prev, '⚠ Connection lost, retrying...']);
        if (retryRef.current < 3) {
          retryRef.current++;
          setTimeout(connect, 2000);
        } else {
          setError('Connection lost');
        }
      };

      return es;
    };

    const es = connect();
    return () => { if (es) es.close(); };
  }, [onReady]);

  const currentStep = steps.length > 0 ? steps[steps.length - 1].step : 0;
  const progress = Math.round((currentStep / 5) * 100);

  return (
    <div className="h-screen bg-[#0A0A0A] flex items-center justify-center px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-6">
          <div className="text-4xl mb-3">🚀</div>
          <h2 className="text-white text-lg font-medium">Setting up your workspace</h2>
          <p className="text-zinc-500 text-sm mt-1">{elapsed}s elapsed</p>
        </div>

        {/* Progress bar */}
        <div className="w-full h-1.5 bg-zinc-800 rounded-full mb-6 overflow-hidden">
          <div
            className="h-full bg-blue-500 rounded-full transition-all duration-500"
            style={{ width: `${progress}%` }}
          />
        </div>

        {/* Steps */}
        <div className="space-y-2.5 mb-6">
          {LABELS.map((label, i) => {
            const s = steps.find(s => s.step === i + 1);
            const isDone = s && (s.status === 'done' || (s.step < currentStep));
            const isRunning = s && s.status === 'running' && s.step === currentStep;

            return (
              <div key={i} className="flex items-center gap-3">
                <div className="w-6 h-6 flex items-center justify-center">
                  {isDone ? (
                    <span className="text-emerald-400">✓</span>
                  ) : isRunning ? (
                    <div className="w-4 h-4 border-2 border-blue-500/30 border-t-blue-400 rounded-full animate-spin" />
                  ) : (
                    <span className="text-zinc-700 text-xs">{i + 1}</span>
                  )}
                </div>
                <span className={`text-sm ${isDone ? 'text-zinc-400' : isRunning ? 'text-white' : 'text-zinc-600'}`}>
                  {label}
                </span>
              </div>
            );
          })}
        </div>

        {/* Log output */}
        <div className="bg-zinc-900/50 border border-zinc-800 rounded-lg p-3 max-h-32 overflow-y-auto font-mono text-xs">
          {logs.length === 0 ? (
            <span className="text-zinc-600">Connecting...</span>
          ) : (
            logs.map((l, i) => (
              <div key={i} className="text-zinc-500 leading-5">{l}</div>
            ))
          )}
          <div ref={logEndRef} />
        </div>

        {error && (
          <div className="mt-4 p-3 bg-red-500/10 border border-red-500/20 rounded-xl">
            <p className="text-red-400 text-sm text-center">{error}</p>
          </div>
        )}
      </div>
    </div>
  );
}
