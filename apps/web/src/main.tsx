import { createRoot } from "react-dom/client";

import { ConsoleAccess } from "./features/first-run/ConsoleAccess";
import { ConsoleApp } from "./ConsoleApp";
import { ConsoleMotion } from "./components/motion";
import { ToastProvider } from "./components/Toast";
import { ThemeProvider } from "./lib/ThemeProvider";
import "./i18n";
import "./styles/app.css";

const root = document.getElementById("root");

if (!root) throw new Error("Missing #root element.");

createRoot(root).render(
  <ThemeProvider>
    <ConsoleMotion>
      <ToastProvider>
        <ConsoleAccess><ConsoleApp /></ConsoleAccess>
      </ToastProvider>
    </ConsoleMotion>
  </ThemeProvider>,
);
