import { Layers3, Server } from "lucide-react";
export type SystemView = "system" | "sandbox";
export function SystemNavigation({ active, onSelect }: { active: SystemView | null; onSelect: (view: SystemView) => void }) {
  return <nav className="main-nav" aria-label="System navigation">
    <p className="nav-label">System</p>
    {([{ id: "system", label: "System", icon: Layers3 }, { id: "sandbox", label: "Hosted Sandbox Manager", icon: Server }] as const).map(({ id, label, icon: Icon }) => (
      <button key={id} type="button" className={active === id ? "active" : ""} onClick={() => onSelect(id)} aria-label={label} aria-current={active === id ? "page" : undefined}>
        <Icon size={15} strokeWidth={1.5} aria-hidden="true" /><span>{label}</span>
      </button>
    ))}
  </nav>;
}
