import { Link } from 'react-router-dom';
import { Layout } from '@/components/layout/Layout';
import { getAllTools } from '@/config/tools';
import { ToolCard } from '@/components/home/ToolCard';
import { ArrowLeftRight } from 'lucide-react';

const ConvertPage = () => {
  const convertTools = getAllTools().filter((tool) => tool.category === 'convert');

  return (
    <Layout>
      <section className="py-12 md:py-20">
        <div className="container">
          {/* Header */}
          <div className="text-center max-w-2xl mx-auto mb-12">
            <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-tool-convert/10 mb-6">
              <ArrowLeftRight className="w-8 h-8 text-tool-convert" />
            </div>
            <h1 className="text-3xl md:text-4xl font-bold mb-4">
              PDF Conversion Tools
            </h1>
            <p className="text-lg text-muted-foreground">
              Convert PDFs to and from Word, Excel, and images. 
              Fast, accurate, and completely free.
            </p>
          </div>

          {/* Tools grid */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6 max-w-4xl mx-auto">
            {convertTools.map((tool) => (
              <ToolCard key={tool.id} tool={tool} />
            ))}
          </div>
        </div>
      </section>
    </Layout>
  );
};

export default ConvertPage;
