import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import { ArrowRight, Shield, Zap, Clock } from 'lucide-react';

const features = [
  {
    icon: Zap,
    label: 'Lightning fast',
  },
  {
    icon: Shield,
    label: 'Secure & private',
  },
  {
    icon: Clock,
    label: 'Auto-delete in 1hr',
  },
];

export const Hero = () => {
  return (
    <section className="relative overflow-hidden bg-gradient-hero">
      {/* Background decoration */}
      <div className="absolute inset-0 overflow-hidden">
        <div className="absolute -top-1/2 -right-1/4 w-[800px] h-[800px] rounded-full bg-primary/5 blur-3xl" />
        <div className="absolute -bottom-1/2 -left-1/4 w-[600px] h-[600px] rounded-full bg-primary/3 blur-3xl" />
      </div>

      <div className="container relative py-20 md:py-32">
        <div className="max-w-3xl mx-auto text-center">
          {/* Badge */}
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-primary/10 text-primary text-sm font-medium mb-6 animate-fade-in">
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-primary opacity-75"></span>
              <span className="relative inline-flex rounded-full h-2 w-2 bg-primary"></span>
            </span>
            Free online PDF tools
          </div>

          {/* Headline */}
          <h1 className="text-4xl md:text-5xl lg:text-6xl font-extrabold tracking-tight mb-6 animate-slide-up">
            Every PDF tool you need,{' '}
            <span className="gradient-text">all in one place</span>
          </h1>

          {/* Subheadline */}
          <p className="text-lg md:text-xl text-muted-foreground mb-8 max-w-2xl mx-auto animate-slide-up [animation-delay:100ms]">
            Merge, split, compress, convert, rotate, and unlock PDF files with ease.
            No installation required. 100% free and secure.
          </p>

          {/* CTA Buttons */}
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mb-12 animate-slide-up [animation-delay:200ms]">
            <Button
              size="lg"
              className="bg-gradient-primary hover:opacity-90 transition-opacity text-base h-12 px-8"
              asChild
            >
              <Link to="/#tools">
                Explore All Tools
                <ArrowRight className="ml-2 h-4 w-4" />
              </Link>
            </Button>
            <Button
              variant="outline"
              size="lg"
              className="text-base h-12 px-8"
              asChild
            >
              <Link to="/merge">Try Merge PDF</Link>
            </Button>
          </div>

          {/* Trust indicators */}
          <div className="flex flex-wrap items-center justify-center gap-6 md:gap-10 animate-slide-up [animation-delay:300ms]">
            {features.map((feature) => (
              <div
                key={feature.label}
                className="flex items-center gap-2 text-muted-foreground"
              >
                <feature.icon className="w-5 h-5 text-primary" />
                <span className="text-sm font-medium">{feature.label}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
};
