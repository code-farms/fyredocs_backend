import { Layout } from '@/components/layout/Layout';
import { Hero } from '@/components/home/Hero';
import { ToolGrid } from '@/components/home/ToolGrid';
import { FeaturesSection } from '@/components/home/FeaturesSection';

const Index = () => {
  return (
    <Layout>
      {/* Hero Section */}
      <Hero />

      {/* All Tools Section */}
      <section id="tools" className="py-16 md:py-24">
        <div className="container">
          <div className="text-center max-w-2xl mx-auto mb-12">
            <h2 className="text-3xl md:text-4xl font-bold mb-4">
              All PDF Tools
            </h2>
            <p className="text-lg text-muted-foreground">
              Choose from our comprehensive collection of PDF tools. 
              All tools are free, fast, and secure.
            </p>
          </div>

          <ToolGrid />
        </div>
      </section>

      {/* Features Section */}
      <FeaturesSection />

      {/* CTA Section */}
      <section className="py-16 md:py-24">
        <div className="container">
          <div className="max-w-3xl mx-auto text-center p-8 md:p-12 rounded-3xl bg-gradient-to-br from-primary/10 via-primary/5 to-transparent border">
            <h2 className="text-2xl md:text-3xl font-bold mb-4">
              Ready to work with your PDFs?
            </h2>
            <p className="text-muted-foreground mb-6">
              No signup required. Start using our tools right away.
            </p>
            <a
              href="#tools"
              className="inline-flex items-center justify-center h-12 px-8 text-base font-medium rounded-lg bg-gradient-primary text-primary-foreground hover:opacity-90 transition-opacity"
            >
              Get Started Free
            </a>
          </div>
        </div>
      </section>
    </Layout>
  );
};

export default Index;
