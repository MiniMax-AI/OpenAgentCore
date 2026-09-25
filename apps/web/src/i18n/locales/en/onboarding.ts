export const onboarding = {
  steps: {
    label: "Setup progress",
    project: "Project and API key",
    tour: "Your console",
  },
  stage: {
    login: { title: "One Core. Many Agents.", body: "Connect your machines, run your Agents, and watch every Session from one console." },
    project: { title: "A project holds the work.", body: "Agents, Sessions, Skills, files and Vaults belong to a project. Applications reach it with the project's API keys." },
    orbit: "Core and what it manages: Agents, Sessions, Skills, Vaults, files, templates and machines",
  },
  terminal: "Try it in a terminal",
  tour: {
    eyebrow: "Your console · {{n}} of {{total}}",
    skip: "Skip",
    back: "Back",
    next: "Next",
    enter: "Open the console",
    shot: "The {{name}} pages of the console",
    chapters: {
      monitor: {
        name: "Monitor",
        title: "Is it healthy, and where does it fail?",
        points: [
          "Overview: service status, running Sessions, sandbox capacity and the Sessions that need you.",
          "Core, Agent and Sandbox metrics: the execution queue, requests and errors, nodes and Runtimes.",
          "Session log: every Session's conversation, trace and Turns, read-only.",
        ],
      },
      resources: {
        name: "Resources",
        title: "Everything your applications created.",
        points: [
          "Agents, environment templates and Skills, with who created each one.",
          "Files and Vaults; credentials stay write-only.",
          "Inspect any asset, or delete one safely.",
        ],
      },
      platform: {
        name: "Platform",
        title: "The deployment itself.",
        points: [
          "Projects and keys: create projects, issue project API keys shown once, revoke or archive.",
          "Nodes: add your machines with one command and watch their capacity.",
          "System: the sandbox configuration every project shares.",
        ],
      },
    },
  },
} as const;
