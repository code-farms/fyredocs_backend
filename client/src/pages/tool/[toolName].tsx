import { useParams, useLocation } from "wouter";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import FileUpload from "@/components/file-upload";
import ProcessingQueue from "@/components/processing-queue";
import { TOOLS } from "@/lib/types";
import { ArrowLeft, FileText } from "lucide-react";

export default function ToolPage() {
  const { toolName } = useParams();
  const [, navigate] = useLocation();
  
  const tool = TOOLS.find(t => t.id === toolName);
  
  if (!tool) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <Card className="w-full max-w-md mx-4">
          <CardContent className="pt-6 text-center">
            <h1 className="text-2xl font-bold text-foreground mb-4">Tool Not Found</h1>
            <p className="text-muted-foreground mb-6">The requested tool could not be found.</p>
            <Button onClick={() => navigate("/")} data-testid="back-to-home">
              <ArrowLeft className="w-4 h-4 mr-2" />
              Back to Home
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background">
      {/* Header */}
      <header className="bg-card border-b border-border sticky top-0 z-50 backdrop-blur-sm bg-card/95">
        <div className="container mx-auto px-4 py-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-4">
              <Button 
                variant="ghost" 
                size="sm"
                onClick={() => navigate("/")}
                data-testid="back-button"
              >
                <ArrowLeft className="w-4 h-4 mr-2" />
                Back
              </Button>
              
              <div className="flex items-center space-x-2">
                <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center">
                  <FileText className="text-primary-foreground" size={24} />
                </div>
                <span className="text-2xl font-bold text-foreground">DocuFlow</span>
              </div>
            </div>
            
            <div className="flex items-center space-x-4">
              <Button variant="ghost" data-testid="sign-in-button">Sign In</Button>
              <Button data-testid="get-started-button">Get Started</Button>
            </div>
          </div>
        </div>
      </header>

      {/* Tool Header */}
      <section className="py-16 bg-gradient-to-r from-primary/10 to-accent/10">
        <div className="container mx-auto px-4">
          <div className="max-w-4xl mx-auto text-center">
            <div className="flex justify-center mb-6">
              <div className={`w-20 h-20 rounded-2xl flex items-center justify-center ${tool.bgColor}`}>
                <i className={`${tool.icon} ${tool.iconColor} text-3xl`} />
              </div>
            </div>
            
            <h1 className="text-4xl md:text-5xl font-bold text-foreground mb-4">
              {tool.name}
            </h1>
            <p className="text-xl text-muted-foreground mb-6 max-w-2xl mx-auto">
              {tool.description}. Fast, secure, and free online tool with no software installation required.
            </p>
            
            <div className="flex flex-wrap justify-center gap-2">
              <Badge variant="secondary">Free to use</Badge>
              <Badge variant="secondary">No registration required</Badge>
              <Badge variant="secondary">Secure processing</Badge>
              <Badge variant="secondary">High quality output</Badge>
            </div>
          </div>
        </div>
      </section>

      {/* Upload Section */}
      <section className="py-16 bg-background">
        <div className="container mx-auto px-4">
          <div className="max-w-4xl mx-auto">
            <div className="text-center mb-12">
              <h2 className="text-3xl font-bold text-foreground mb-4">
                Upload Your Files
              </h2>
              <p className="text-lg text-muted-foreground">
                Select or drag and drop your files to get started with {tool.name.toLowerCase()}
              </p>
            </div>
            
            <FileUpload 
              toolType={tool.id}
              acceptedFileTypes={getAcceptedFileTypes(tool.id)}
              maxFiles={tool.id.includes('merge') ? 10 : 1}
            />
          </div>
        </div>
      </section>

      {/* Processing Queue */}
      <section className="py-16 bg-muted/50">
        <div className="container mx-auto px-4">
          <div className="max-w-4xl mx-auto">
            <ProcessingQueue />
          </div>
        </div>
      </section>

      {/* Tool Info */}
      <section className="py-16 bg-background">
        <div className="container mx-auto px-4">
          <div className="max-w-4xl mx-auto">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
              <Card>
                <CardHeader>
                  <CardTitle>How it works</CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div className="flex items-start space-x-3">
                    <div className="w-8 h-8 bg-primary rounded-full flex items-center justify-center text-primary-foreground font-semibold text-sm">
                      1
                    </div>
                    <div>
                      <h4 className="font-semibold text-foreground">Upload your files</h4>
                      <p className="text-sm text-muted-foreground">Select files from your device or drag and drop them</p>
                    </div>
                  </div>
                  
                  <div className="flex items-start space-x-3">
                    <div className="w-8 h-8 bg-accent rounded-full flex items-center justify-center text-accent-foreground font-semibold text-sm">
                      2
                    </div>
                    <div>
                      <h4 className="font-semibold text-foreground">Automatic processing</h4>
                      <p className="text-sm text-muted-foreground">Our servers process your files instantly with high quality</p>
                    </div>
                  </div>
                  
                  <div className="flex items-start space-x-3">
                    <div className="w-8 h-8 bg-orange-500 rounded-full flex items-center justify-center text-white font-semibold text-sm">
                      3
                    </div>
                    <div>
                      <h4 className="font-semibold text-foreground">Download results</h4>
                      <p className="text-sm text-muted-foreground">Get your processed files ready for download</p>
                    </div>
                  </div>
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>Features & Benefits</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">High-quality conversion with format preservation</span>
                  </div>
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">Fast processing with cloud infrastructure</span>
                  </div>
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">Secure file handling with automatic deletion</span>
                  </div>
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">No software installation required</span>
                  </div>
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">Works on all devices and platforms</span>
                  </div>
                  <div className="flex items-center space-x-3">
                    <i className="fas fa-check text-accent"></i>
                    <span className="text-sm text-foreground">Free to use with no limits</span>
                  </div>
                </CardContent>
              </Card>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

function getAcceptedFileTypes(toolId: string): string[] {
  const pdfTools = ['merge-pdf', 'split-pdf', 'compress-pdf', 'edit-pdf', 'protect-pdf', 'unlock-pdf', 'sign-pdf', 'watermark-pdf'];
  const convertFromPdf = ['pdf-to-word', 'pdf-to-excel', 'pdf-to-powerpoint'];
  const convertToPdf = ['word-to-pdf', 'excel-to-pdf', 'powerpoint-to-pdf'];

  if (pdfTools.includes(toolId) || convertFromPdf.includes(toolId)) {
    return ['.pdf'];
  }
  
  if (toolId === 'word-to-pdf') {
    return ['.doc', '.docx'];
  }
  
  if (toolId === 'excel-to-pdf') {
    return ['.xls', '.xlsx'];
  }
  
  if (toolId === 'powerpoint-to-pdf') {
    return ['.ppt', '.pptx'];
  }
  
  // Default to common document types
  return ['.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx'];
}
