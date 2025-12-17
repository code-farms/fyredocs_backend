import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import FileUpload from "@/components/file-upload";
import ProcessingQueue from "@/components/processing-queue";
import ToolCard from "@/components/tool-card";
import { TOOLS, TOOL_CATEGORIES } from "@/lib/types";
import { FileText, Upload, Download, Shield, Zap, Smartphone, Eye, Cloud, Headphones } from "lucide-react";

export default function Home() {
  const [showUpload, setShowUpload] = useState(false);

  const convertTools = TOOLS.filter(tool => tool.category === 'convert');
  const organizeTools = TOOLS.filter(tool => tool.category === 'organize');
  const securityTools = TOOLS.filter(tool => tool.category === 'security');

  return (
    <div className="min-h-screen bg-background">
      {/* Header */}
      <header className="bg-card border-b border-border sticky top-0 z-50 backdrop-blur-sm bg-card/95">
        <div className="container mx-auto px-4 py-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center">
                <FileText className="text-primary-foreground" size={24} />
              </div>
              <span className="text-2xl font-bold text-foreground">DocuFlow</span>
            </div>
            
            <nav className="hidden md:flex items-center space-x-8">
              <a href="#tools" className="text-muted-foreground hover:text-foreground transition-colors">Tools</a>
              <a href="#features" className="text-muted-foreground hover:text-foreground transition-colors">Features</a>
              <a href="#pricing" className="text-muted-foreground hover:text-foreground transition-colors">Pricing</a>
              <a href="#help" className="text-muted-foreground hover:text-foreground transition-colors">Help</a>
            </nav>

            <div className="flex items-center space-x-4">
              <Button variant="ghost" data-testid="sign-in-button">Sign In</Button>
              <Button data-testid="get-started-button">Get Started</Button>
            </div>
          </div>
        </div>
      </header>

      {/* Hero Section */}
      <section className="hero-bg py-20">
        <div className="container mx-auto px-4 text-center">
          <h1 className="text-5xl md:text-6xl font-bold text-white mb-6">
            Every PDF Tool You Need
          </h1>
          <p className="text-xl text-white/90 mb-8 max-w-2xl mx-auto">
            Convert, merge, split, compress and edit your PDF files with our professional-grade tools. 
            No installation required, 100% secure processing.
          </p>

          {/* Upload Area */}
          <div className="max-w-2xl mx-auto">
            {!showUpload ? (
              <div 
                className="drag-zone bg-white/10 backdrop-blur-md border-2 border-dashed border-white/30 rounded-2xl p-12 mb-8 cursor-pointer"
                onClick={() => setShowUpload(true)}
                data-testid="main-upload-trigger"
              >
                <div className="text-center">
                  <div className="w-16 h-16 bg-white/20 rounded-full flex items-center justify-center mx-auto mb-4">
                    <Upload className="text-white" size={32} />
                  </div>
                  <h3 className="text-2xl font-semibold text-white mb-2">Drop files here</h3>
                  <p className="text-white/80 mb-4">or click to browse</p>
                  <Button 
                    className="bg-white text-primary hover:bg-white/90"
                    data-testid="choose-files-main"
                  >
                    Choose Files
                  </Button>
                </div>
              </div>
            ) : (
              <FileUpload 
                toolType="general" 
                onUploadComplete={() => setShowUpload(false)}
                className="mb-8"
              />
            )}

            <div className="flex flex-wrap justify-center gap-4 text-sm text-white/80">
              <span><Shield className="w-4 h-4 inline mr-1" />256-bit SSL encryption</span>
              <span><i className="fas fa-clock mr-1"></i>Files deleted after 1 hour</span>
              <span><i className="fas fa-check mr-1"></i>GDPR compliant</span>
            </div>
          </div>
        </div>
      </section>

      {/* Processing Queue */}
      <section className="py-12 bg-background">
        <div className="container mx-auto px-4">
          <ProcessingQueue />
        </div>
      </section>

      {/* Tools Section */}
      <section id="tools" className="py-20 bg-background">
        <div className="container mx-auto px-4">
          <div className="text-center mb-16">
            <h2 className="text-4xl font-bold text-foreground mb-4">PDF Tools for Every Need</h2>
            <p className="text-xl text-muted-foreground max-w-2xl mx-auto">
              Professional document processing tools trusted by millions of users worldwide
            </p>
          </div>

          {/* Convert Tools */}
          <div className="mb-16">
            <h3 className="text-2xl font-bold text-foreground mb-8 flex items-center">
              <div className="w-8 h-8 bg-primary/10 rounded-lg flex items-center justify-center mr-3">
                <i className="fas fa-exchange-alt text-primary"></i>
              </div>
              {TOOL_CATEGORIES.convert.title}
            </h3>
            
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
              {convertTools.map((tool) => (
                <ToolCard key={tool.id} tool={tool} />
              ))}
            </div>
          </div>

          {/* Organize Tools */}
          <div className="mb-16">
            <h3 className="text-2xl font-bold text-foreground mb-8 flex items-center">
              <div className="w-8 h-8 bg-accent/10 rounded-lg flex items-center justify-center mr-3">
                <i className="fas fa-layer-group text-accent"></i>
              </div>
              {TOOL_CATEGORIES.organize.title}
            </h3>
            
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
              {organizeTools.map((tool) => (
                <ToolCard key={tool.id} tool={tool} />
              ))}
            </div>
          </div>

          {/* Security Tools */}
          <div className="mb-16">
            <h3 className="text-2xl font-bold text-foreground mb-8 flex items-center">
              <div className="w-8 h-8 bg-destructive/10 rounded-lg flex items-center justify-center mr-3">
                <Shield className="text-destructive" size={20} />
              </div>
              {TOOL_CATEGORIES.security.title}
            </h3>
            
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
              {securityTools.map((tool) => (
                <ToolCard key={tool.id} tool={tool} />
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* Features Section */}
      <section id="features" className="py-20 bg-muted/50">
        <div className="container mx-auto px-4">
          <div className="text-center mb-16">
            <h2 className="text-4xl font-bold text-foreground mb-4">Why Choose DocuFlow?</h2>
            <p className="text-xl text-muted-foreground max-w-2xl mx-auto">
              Professional-grade document processing with enterprise security and reliability
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8">
            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-primary/10 rounded-xl flex items-center justify-center mb-6">
                  <Zap className="text-primary" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">Lightning Fast</h3>
                <p className="text-muted-foreground">
                  Process documents in seconds with our optimized cloud infrastructure. Batch processing support for multiple files.
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-accent/10 rounded-xl flex items-center justify-center mb-6">
                  <Shield className="text-accent" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">100% Secure</h3>
                <p className="text-muted-foreground">
                  Bank-level encryption protects your files. Automatic deletion after processing ensures your privacy.
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-orange-100 rounded-xl flex items-center justify-center mb-6">
                  <Smartphone className="text-orange-600" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">Any Device</h3>
                <p className="text-muted-foreground">
                  Works perfectly on desktop, tablet, and mobile. No software installation required.
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-purple-100 rounded-xl flex items-center justify-center mb-6">
                  <Eye className="text-purple-600" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">Perfect Quality</h3>
                <p className="text-muted-foreground">
                  Advanced algorithms preserve document formatting, fonts, and layouts with 100% accuracy.
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-teal-100 rounded-xl flex items-center justify-center mb-6">
                  <Cloud className="text-teal-600" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">Cloud Integration</h3>
                <p className="text-muted-foreground">
                  Direct integration with Google Drive, Dropbox, and OneDrive for seamless workflow.
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardContent className="p-8">
                <div className="w-16 h-16 bg-pink-100 rounded-xl flex items-center justify-center mb-6">
                  <Headphones className="text-pink-600" size={32} />
                </div>
                <h3 className="text-xl font-semibold text-foreground mb-4">24/7 Support</h3>
                <p className="text-muted-foreground">
                  Expert customer support available around the clock. API documentation and developer resources.
                </p>
              </CardContent>
            </Card>
          </div>
        </div>
      </section>

      {/* CTA Section */}
      <section className="py-20 bg-primary">
        <div className="container mx-auto px-4 text-center">
          <h2 className="text-4xl font-bold text-primary-foreground mb-6">
            Ready to Process Your Documents?
          </h2>
          <p className="text-xl text-primary-foreground/90 mb-8 max-w-2xl mx-auto">
            Join millions of users who trust DocuFlow for their document processing needs. 
            Start with our free tools or upgrade for unlimited access.
          </p>

          <div className="flex flex-col sm:flex-row justify-center gap-4 mb-12">
            <Button 
              size="lg"
              className="bg-white text-primary hover:bg-white/90"
              data-testid="start-free-trial"
            >
              Start Free Trial
            </Button>
            <Button 
              size="lg"
              variant="outline"
              className="border-white text-white hover:bg-white/10"
              data-testid="view-pricing"
            >
              View Pricing
            </Button>
          </div>

          <div className="flex flex-wrap justify-center gap-8 text-primary-foreground/80">
            <span><i className="fas fa-check mr-2"></i>No credit card required</span>
            <span><i className="fas fa-check mr-2"></i>14-day free trial</span>
            <span><i className="fas fa-check mr-2"></i>Cancel anytime</span>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="bg-card border-t border-border py-16">
        <div className="container mx-auto px-4">
          <div className="grid grid-cols-1 md:grid-cols-4 gap-8">
            <div className="space-y-4">
              <div className="flex items-center space-x-2">
                <div className="w-8 h-8 bg-primary rounded-lg flex items-center justify-center">
                  <FileText className="text-primary-foreground" size={20} />
                </div>
                <span className="text-xl font-bold text-foreground">DocuFlow</span>
              </div>
              <p className="text-muted-foreground">
                Professional document processing platform trusted by millions worldwide.
              </p>
            </div>

            <div>
              <h5 className="font-semibold text-foreground mb-4">Tools</h5>
              <ul className="space-y-2 text-muted-foreground">
                <li><a href="#" className="hover:text-foreground transition-colors">PDF to Word</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">PDF to Excel</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Merge PDF</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Compress PDF</a></li>
              </ul>
            </div>

            <div>
              <h5 className="font-semibold text-foreground mb-4">Company</h5>
              <ul className="space-y-2 text-muted-foreground">
                <li><a href="#" className="hover:text-foreground transition-colors">About Us</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Blog</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Careers</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Contact</a></li>
              </ul>
            </div>

            <div>
              <h5 className="font-semibold text-foreground mb-4">Support</h5>
              <ul className="space-y-2 text-muted-foreground">
                <li><a href="#" className="hover:text-foreground transition-colors">Help Center</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Privacy Policy</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Terms of Service</a></li>
                <li><a href="#" className="hover:text-foreground transition-colors">Security</a></li>
              </ul>
            </div>
          </div>

          <div className="border-t border-border pt-8 mt-8">
            <div className="flex flex-col md:flex-row justify-between items-center">
              <p className="text-muted-foreground">
                © 2024 DocuFlow. All rights reserved.
              </p>
              <div className="flex space-x-6 mt-4 md:mt-0">
                <a href="#" className="text-muted-foreground hover:text-foreground transition-colors text-sm">Privacy</a>
                <a href="#" className="text-muted-foreground hover:text-foreground transition-colors text-sm">Terms</a>
                <a href="#" className="text-muted-foreground hover:text-foreground transition-colors text-sm">Cookies</a>
              </div>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}
