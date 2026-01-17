import { Shield, Zap, Cloud, Lock, RefreshCw, Globe } from 'lucide-react';

const features = [
  {
    icon: Zap,
    title: 'Lightning Fast',
    description:
      'Process your PDFs in seconds with our optimized cloud infrastructure. No waiting, no delays.',
  },
  {
    icon: Shield,
    title: 'Bank-Level Security',
    description:
      'All files are encrypted with 256-bit SSL. Your documents are safe and protected.',
  },
  {
    icon: Cloud,
    title: 'Cloud Processing',
    description:
      'Heavy lifting happens on our servers. No software to install, works on any device.',
  },
  {
    icon: Lock,
    title: 'Privacy First',
    description:
      'Files are automatically deleted after 1 hour. We never access or store your content.',
  },
  {
    icon: RefreshCw,
    title: 'Resumable Uploads',
    description:
      'Connection dropped? No problem. Resume your upload right where you left off.',
  },
  {
    icon: Globe,
    title: 'Works Everywhere',
    description:
      'Access from any browser on any device. Desktop, tablet, or mobile.',
  },
];

export const FeaturesSection = () => {
  return (
    <section className="py-20 md:py-28 bg-muted/30">
      <div className="container">
        {/* Section header */}
        <div className="text-center max-w-2xl mx-auto mb-16">
          <h2 className="text-3xl md:text-4xl font-bold mb-4">
            Why choose our PDF tools?
          </h2>
          <p className="text-lg text-muted-foreground">
            Built for speed, security, and simplicity. Everything you need to work
            with PDFs efficiently.
          </p>
        </div>

        {/* Features grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8">
          {features.map((feature) => (
            <div
              key={feature.title}
              className="group p-6 rounded-2xl bg-card border hover:border-primary/20 hover:shadow-lg transition-all duration-300"
            >
              <div className="w-12 h-12 rounded-xl bg-primary/10 flex items-center justify-center mb-4 group-hover:bg-primary/20 transition-colors">
                <feature.icon className="w-6 h-6 text-primary" />
              </div>
              <h3 className="font-semibold text-lg mb-2">{feature.title}</h3>
              <p className="text-muted-foreground">{feature.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
};
