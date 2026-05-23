import { useEffect, useState } from "react";
import "./Toast.css";

let pushImpl = () => {};
export function pushToast(msg, kind = "info") { pushImpl({ msg, kind, id: Date.now() + Math.random() }); }

export default function Toast() {
  const [items, setItems] = useState([]);
  useEffect(() => {
    pushImpl = (it) => {
      setItems((prev) => [...prev, it]);
      setTimeout(() => setItems((prev) => prev.filter((x) => x.id !== it.id)), 2400);
    };
    return () => { pushImpl = () => {}; };
  }, []);
  return (
    <div className="toast-stack">
      {items.map((it) => (
        <div key={it.id} className={`toast toast--${it.kind}`}>{it.msg}</div>
      ))}
    </div>
  );
}
