import { ReactNode, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import FileUpload from "@/components/file-upload";
import ProcessingQueue from "@/components/processing-queue";
import ToolCard from "@/components/tool-card";
import { TOOLS, TOOL_CATEGORIES } from "@/lib/types";
import { ThemeToggle } from "@/components/theme-toggle";
import {
  ArrowRight,
  Check,
  Cloud,
  FileText,
  Shield,
  Sparkles,
  Upload,
  Zap,
} from "lucide-react";

const highlights = [
  { label: "New", text: "AI-precise conversions" },
  { label: "Security", text: "Zero-retention processing" },
  { label: "Speed", text: "<8s avg. turnaround" },
];

const pillars = [
  {
    icon: <Zap className="text-primary" size={24} />,
    title: "Instant speed",
    description: "GPU-accelerated processing with smart batching to keep you in flow.",
  },
  {
    icon: <Shield className="text-accent" size={24} />,
    title: "Enterprise-grade",
    description: "256-bit encryption, signed URLs, and auto-deletion once you’re done.",
  },
  {
    icon: <Sparkles className="text-primary" size={24} />,
    title: "Polished output",
    description: "Layout-safe conversions that protect typography, tables, and media.",
  },
];

export default function Home() {
  const [showUpload, setShowUpload] = useState(false);
  const [autoOpenKey, setAutoOpenKey] = useState(0);

  const convertTools = TOOLS.filter((tool) => tool.category === "convert");
  const organizeTools = TOOLS.filter((tool) => tool.category === "organize");
  const securityTools = TOOLS.filter((tool) => tool.category === "security");

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-50 backdrop-blur-xl bg-background/80 border-b border-border/80">
        <div className="max-w-6xl mx-auto px-4 py-4 flex items-center justify-between">
          <div className="flex items-center space-x-3">
            <div className="h-11 w-11 rounded-xl bg-gradient-to-br from-primary to-accent flex items-center justify-center shadow-lg">
              <FileText className="text-primary-foreground" size={22} />
            </div>
            <div>
              <p className="text-lg font-semibold tracking-tight">EsyDocs</p>
              <p className="text-xs text-muted-foreground">PDF OS for teams</p>
            </div>
          </div>
          <nav className="hidden md:flex items-center gap-6 text-sm">
            <a href="#tools" className="text-muted-foreground hover:text-foreground transition-colors">
              Tools
            </a>
            <a href="#workflow" className="text-muted-foreground hover:text-foreground transition-colors">
              Workflow
            </a>
            <a href="#features" className="text-muted-foreground hover:text-foreground transition-colors">
              Why us
            </a>
          </nav>
          <div className="flex items-center gap-3">
            <ThemeToggle />
            <Button variant="ghost" size="sm" data-testid="sign-in-button">
              Sign in
            </Button>
            <Button size="sm" className="gap-2" data-testid="get-started-button">
              Launch Studio <ArrowRight size={16} />
            </Button>
          </div>
        </div>
      </header>

      <div className="grid grid-cols-1 xl:grid-cols-[1fr_2fr_1fr] min-h-screen">
        <AdRail position="left" />

        <div className="flex flex-col min-h-screen border-x border-border/60">
          <main className="flex-1">
            <section className="hero-bg text-white relative overflow-hidden">
              <div className="max-w-5xl mx-auto px-4 py-16 lg:py-24 relative">
                <div className="grid lg:grid-cols-1 gap-10 items-start">
                  <div className="space-y-6 text-center lg:text-left">
                    <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-white/10 text-white/80 border border-white/15">
                      <Sparkles size={16} /> New: Neon workspace for PDFs
                    </div>
                    <div className="space-y-4">
                      <h1 className="text-4xl md:text-5xl xl:text-6xl font-semibold leading-tight tracking-tight">
                        The fastest path from messy PDF to finished doc.
                      </h1>
                      <p className="text-lg text-white/80 max-w-3xl mx-auto lg:mx-0">
                    Upload once, choose the action, and let EsyDocs auto-tune splits, merges, protection, and export quality. Built for velocity, designed for clarity.
                      </p>
                    </div>
                    <div className="flex flex-wrap gap-3 justify-center lg:justify-start">
                      <Button
                        size="lg"
                        className="gap-2"
                        onClick={() => {
                          setShowUpload(true);
                          setAutoOpenKey((k) => k + 1);
                        }}
                        data-testid="choose-files-main"
                      >
                        Start with a file <Upload size={18} />
                      </Button>
                      <Button size="lg" variant="outline" className="border-white/30 text-white hover:bg-white/10">
                        Watch demo <ArrowRight size={18} />
                      </Button>
                    </div>
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 max-w-lg mx-auto lg:mx-0">
                      {[
                        { label: "Avg. turnaround", value: "7.8s" },
                        { label: "Secure sessions", value: "256-bit" },
                        { label: "Satisfaction", value: "4.9/5" },
                      ].map((stat) => (
                        <Card key={stat.label} className="glass-panel border-white/10 bg-white/5">
                          <CardContent className="p-4">
                            <p className="text-sm text-white/70">{stat.label}</p>
                            <p className="text-xl font-semibold text-white">{stat.value}</p>
                          </CardContent>
                        </Card>
                      ))}
                    </div>
                    <div className="glass-panel rounded-3xl p-6 lg:p-8 border border-white/15 shadow-xl max-w-xl mx-auto lg:mx-0">
                      <div className="flex items-center justify-between mb-4">
                        <div>
                          <p className="text-sm text-white/70">Upload & process</p>
                          <p className="text-xl font-semibold">Drop PDFs or office docs</p>
                        </div>
                        <Badge className="bg-white/10 text-white border-white/20">Live</Badge>
                      </div>
                      {showUpload ? (
                        <FileUpload
                          toolType="general"
                          onUploadComplete={() => setShowUpload(false)}
                          className="mb-6"
                          autoOpenKey={autoOpenKey}
                        />
                      ) : (
                        <div
                          className="drag-zone border-2 border-dashed border-white/20 rounded-2xl p-10 text-center cursor-pointer bg-white/5"
                          onClick={() => {
                            setShowUpload(true);
                            setAutoOpenKey((k) => k + 1);
                          }}
                          data-testid="main-upload-trigger"
                        >
                          <div className="flex flex-col items-center space-y-3">
                            <div className="h-14 w-14 rounded-full bg-white/10 flex items-center justify-center">
                              <Upload size={24} />
                            </div>
                            <div>
                              <p className="text-2xl font-semibold">Drop files here</p>
                              <p className="text-white/70">or click to browse</p>
                            </div>
                          </div>
                        </div>
                      )}
                      <div className="mt-4 flex flex-wrap gap-3 text-xs text-white/70">
                        <span className="inline-flex items-center gap-1">
                          <Shield size={14} /> 256-bit encryption
                        </span>
                        <span className="inline-flex items-center gap-1">
                          <Cloud size={14} /> Auto delete after processing
                        </span>
                        <span className="inline-flex items-center gap-1">
                          <Check size={14} /> GDPR ready
                        </span>
                      </div>
                    </div>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                    {pillars.map((pillar) => (
                      <Card key={pillar.title} className="glass-panel border-white/10 bg-white/5">
                        <CardContent className="p-4 space-y-2">
                          <div className="h-10 w-10 rounded-full bg-white/10 flex items-center justify-center">
                            {pillar.icon}
                          </div>
                          <p className="font-semibold text-white">{pillar.title}</p>
                          <p className="text-sm text-white/70">{pillar.description}</p>
                        </CardContent>
                      </Card>
                    ))}
                  </div>
                </div>
              </div>
            </section>

            <section className="py-12 bg-background">
              <div className="max-w-5xl mx-auto px-4">
                <ProcessingQueue />
              </div>
            </section>

            <section id="tools" className="py-16 max-w-5xl mx-auto px-4 space-y-12">
              <div className="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-6">
                <div className="space-y-3">
                  <Badge variant="outline" className="border-border text-muted-foreground">
                    Toolkit
                  </Badge>
                  <h2 className="text-3xl md:text-4xl font-semibold tracking-tight">Purpose-built PDF tools</h2>
                  <p className="text-muted-foreground max-w-2xl">
                    Curated actions for every document moment: conversion, organization, and ironclad security.
                  </p>
                </div>
                <div className="flex gap-3">
                  <Button variant="outline" className="border-border text-foreground">
                    View roadmap
                  </Button>
                  <Button className="gap-2">
                    Request a tool <ArrowRight size={16} />
                  </Button>
                </div>
              </div>

              <div className="space-y-10">
                <CategoryBlock title={TOOL_CATEGORIES.convert.title} accent="from-primary/40 to-primary/10">
                  {convertTools}
                </CategoryBlock>
                <CategoryBlock title={TOOL_CATEGORIES.organize.title} accent="from-accent/40 to-accent/10">
                  {organizeTools}
                </CategoryBlock>
                <CategoryBlock title={TOOL_CATEGORIES.security.title} accent="from-destructive/30 to-destructive/10">
                  {securityTools}
                </CategoryBlock>
              </div>
            </section>

            <section id="workflow" className="py-16">
              <div className="max-w-5xl mx-auto px-4">
                <div className="glass-panel rounded-3xl p-10 border border-border/60">
                  <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-6 mb-8">
                    <div>
                      <Badge variant="outline" className="border-border text-muted-foreground">
                        Flow
                      </Badge>
                      <h3 className="text-3xl font-semibold mt-3">
                        From messy PDFs to finished docs in minutes
                      </h3>
                      <p className="text-muted-foreground mt-2 max-w-2xl">
                        A guided, multi-step runway that keeps you on task while we automate the heavy lifting.
                      </p>
                    </div>
                    <Button variant="secondary" className="gap-2">
                      Build a flow <ArrowRight size={16} />
                    </Button>
                  </div>
                  <div className="grid md:grid-cols-3 gap-6">
                    {[
                      { title: "1. Drop", desc: "Upload from desktop or cloud and pick your intent.", icon: <Upload size={18} /> },
                      { title: "2. Tune", desc: "Apply split/merge, protect, or compress with smart presets.", icon: <Sparkles size={18} /> },
                      { title: "3. Deliver", desc: "Instant downloads and shareable links with audit-friendly logs.", icon: <Check size={18} /> },
                    ].map((step) => (
                      <Card key={step.title} className="bg-card border-border/80">
                        <CardHeader>
                          <div className="h-10 w-10 rounded-full bg-primary/10 text-primary flex items-center justify-center">
                            {step.icon}
                          </div>
                        </CardHeader>
                        <CardContent className="pt-0 space-y-2">
                          <CardTitle>{step.title}</CardTitle>
                          <p className="text-sm text-muted-foreground">{step.desc}</p>
                        </CardContent>
                      </Card>
                    ))}
                  </div>
                </div>
              </div>
            </section>

            <section id="features" className="py-16 bg-secondary/30">
              <div className="max-w-5xl mx-auto px-4">
                <div className="text-center space-y-3 mb-10">
                  <Badge variant="outline" className="border-border text-muted-foreground">
                    Why teams pick EsyDocs
                  </Badge>
                  <h3 className="text-3xl md:text-4xl font-semibold">Modern performance. Zero clutter.</h3>
                  <p className="text-muted-foreground max-w-2xl mx-auto">
                    Built with shadcn/ui and a custom neon palette to keep the interface crisp, fast, and trustworthy.
                  </p>
                </div>
                <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
                  {[
                    {
                      title: "Realtime queue",
                      desc: "Live progress with status-aware cards so you never wonder what's running.",
                      icon: <Zap size={18} />,
                    },
                    {
                      title: "Role-ready",
                      desc: "Guardrails for protected files and compliant audit notes on every action.",
                      icon: <Shield size={18} />,
                    },
                    {
                      title: "Cloud-native",
                      desc: "Optimized for browsers - no plugins, no installs, always the newest build.",
                      icon: <Cloud size={18} />,
                    },
                  ].map((feature) => (
                    <Card key={feature.title} className="bg-card border-border/80">
                      <CardHeader className="flex flex-row items-center gap-3">
                        <div className="h-10 w-10 rounded-full bg-primary/10 text-primary flex items-center justify-center">
                          {feature.icon}
                        </div>
                        <CardTitle className="text-lg">{feature.title}</CardTitle>
                      </CardHeader>
                      <CardContent className="pt-0 text-muted-foreground">{feature.desc}</CardContent>
                    </Card>
                  ))}
                </div>
              </div>
            </section>

            <section className="py-16">
              <div className="max-w-5xl mx-auto px-4">
                <div className="glass-panel rounded-3xl p-10 border border-border/70 text-center space-y-6">
                  <Badge variant="outline" className="border-border text-muted-foreground">
                    Ready?
                  </Badge>
                  <h3 className="text-3xl md:text-4xl font-semibold">
                    Ship dashing PDFs without the busywork.
                  </h3>
                  <p className="text-muted-foreground max-w-2xl mx-auto">
                    Spin up a flow, drag in your files, and let the studio take care of the rest. Your docs, your controls, every time.
                  </p>
                  <div className="flex flex-wrap justify-center gap-3">
                    <Button size="lg" className="gap-2">
                      Launch EsyDocs <ArrowRight size={18} />
                    </Button>
                    <Button size="lg" variant="outline" className="border-border text-foreground">
                      Talk to us
                    </Button>
                  </div>
                </div>
              </div>
            </section>
          </main>
        </div>

        <AdRail position="right" />
      </div>

      <footer className="border-t border-border/70 bg-card/40">
        <div className="max-w-6xl mx-auto px-4 py-8 flex flex-col md:flex-row items-center justify-between gap-4">
          <div className="flex items-center space-x-3">
            <div className="h-9 w-9 rounded-lg bg-gradient-to-br from-primary to-accent flex items-center justify-center">
              <FileText className="text-primary-foreground" size={18} />
            </div>
            <div>
              <p className="font-semibold">EsyDocs</p>
              <p className="text-xs text-muted-foreground">Built for modern document ops.</p>
            </div>
          </div>
          <div className="flex flex-wrap gap-4 text-sm text-muted-foreground">
            <a href="#tools" className="hover:text-foreground">Tools</a>
            <a href="#features" className="hover:text-foreground">Why us</a>
            <a href="#workflow" className="hover:text-foreground">Workflow</a>
            <span>(c) 2024</span>
          </div>
        </div>
      </footer>
    </div>
  );
}

function CategoryBlock({
  title,
  accent,
  children,
}: {
  title: string;
  accent: string;
  children: ReactNode[];
}) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className={`h-10 w-10 rounded-xl bg-gradient-to-br ${accent} flex items-center justify-center`}>
          <Sparkles size={18} className="text-white" />
        </div>
        <h3 className="text-2xl font-semibold">{title}</h3>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        {children.map((tool) => (
          <div key={(tool as any).id} className="tool-card">
            <ToolCard tool={tool as any} />
          </div>
        ))}
      </div>
    </div>
  );
}

function AdRail({ position }: { position: "left" | "right" }) {
  const borderClass = position === "left" ? "border-r" : "border-l";
  return (
    <div className={`hidden xl:block ${borderClass} border-border/60`}>
      <div className="sticky top-20 p-6 space-y-4">
        <AdCard title="Sponsored" />
        <AdCard title="Promoted" />
      </div>
    </div>
  );
}

function AdCard({ title }: { title: string }) {
  return (
    <div className="glass-panel rounded-2xl p-4 border border-border/70 space-y-2">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">{title}</p>
      <div className="h-40 w-full rounded-xl bg-gradient-to-br from-primary/15 via-accent/10 to-transparent border border-border/40" />
      <p className="text-sm text-muted-foreground">Your ad could be here.</p>
    </div>
  );
}
