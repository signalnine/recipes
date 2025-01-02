package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/parser"
	"gopkg.in/yaml.v2"
)

type Recipe struct {
	Slug        string
	Title       string   `yaml:"title"`
	Tags        []string `yaml:"tags"`
	Content     string
	HTMLContent template.HTML
}

type SiteGenerator struct {
	RecipesDir string
	OutputDir  string
	Recipes    []Recipe
	RecipeMap  map[string]string // slug -> title mapping
	s3Client   *s3.Client
	s3Bucket   string
}

func NewSiteGenerator(recipesDir, outputDir string, s3Client *s3.Client, s3Bucket string) *SiteGenerator {
	return &SiteGenerator{
		RecipesDir: recipesDir,
		OutputDir:  outputDir,
		RecipeMap:  make(map[string]string),
		s3Client:   s3Client,
		s3Bucket:   s3Bucket,
	}
}

func (sg *SiteGenerator) Generate() error {
	if err := os.MkdirAll(sg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	if err := sg.collectRecipes(); err != nil {
		return fmt.Errorf("collecting recipes: %w", err)
	}

	if err := sg.generatePages(); err != nil {
		return fmt.Errorf("generating pages: %w", err)
	}

	if sg.s3Client != nil {
		if err := sg.uploadToS3(); err != nil {
			return fmt.Errorf("uploading to S3: %w", err)
		}
	}

	return nil
}

func (sg *SiteGenerator) collectRecipes() error {
	files, err := ioutil.ReadDir(sg.RecipesDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.Name() == "README.md" || filepath.Ext(file.Name()) != ".md" {
			continue
		}

		content, err := ioutil.ReadFile(filepath.Join(sg.RecipesDir, file.Name()))
		if err != nil {
			return err
		}

		var recipe Recipe
		recipe.Slug = strings.TrimSuffix(file.Name(), ".md")
		
		// Replace underscores with spaces in the slug for the default title
		recipe.Title = strings.ReplaceAll(recipe.Slug, "_", " ")
		
		// Look for the first heading in the content
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "# ") {
				recipe.Title = strings.TrimPrefix(line, "# ")
				break
			}
		}

		// Parse frontmatter if it exists
		if bytes.HasPrefix(content, []byte("---")) {
			parts := bytes.SplitN(content, []byte("---"), 3)
			if len(parts) == 3 {
				if err := yaml.Unmarshal(parts[1], &recipe); err != nil {
					fmt.Printf("Warning: Failed to parse frontmatter for %s: %v\n", file.Name(), err)
				}
				recipe.Content = string(parts[2])
			} else {
				recipe.Content = string(content)
			}
		} else {
			recipe.Content = string(content)
		}
		sg.Recipes = append(sg.Recipes, recipe)
		sg.RecipeMap[recipe.Slug] = recipe.Title
	}

	return nil
}

func (sg *SiteGenerator) generatePages() error {
	for i := range sg.Recipes {
		if err := sg.generateRecipePage(&sg.Recipes[i]); err != nil {
			return err
		}
	}
	return sg.generateIndexPage()
}

func (sg *SiteGenerator) generateRecipePage(recipe *Recipe) error {
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs
	parser := parser.NewWithExtensions(extensions)
	html := markdown.ToHTML([]byte(recipe.Content), parser, nil)
	
	content := string(html)
	for slug, title := range sg.RecipeMap {
		content = strings.ReplaceAll(
			content,
			">"+title+"<",
			fmt.Sprintf("><a href=\"%s.html\">%s</a><", slug, title),
		)
	}
	recipe.HTMLContent = template.HTML(content)

	tmpl := template.Must(template.New("recipe").Parse(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Gabe's Recipes - {{.Title}}</title>
    <style>
        :root {
            color-scheme: dark light;
            
            --primary: #E2E8F0;
            --secondary: #CBD5E0;
            --accent: #B794F4;
            --background: #1A202C;
            --surface: #2D3748;
            --text: #F7FAFC;
            --muted: #A0AEC0;
            --border: #4A5568;
        }

        @media (prefers-color-scheme: light) {
            :root {
                --primary: #2D3748;
                --secondary: #4A5568;
                --accent: #553C9A;
                --background: #ffffff;
                --surface: #F7FAFC;
                --text: #1A202C;
                --muted: #718096;
                --border: #E2E8F0;
            }
        }

        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            line-height: 1.7;
            color: var(--text);
            background: var(--background);
            max-width: 800px;
            margin: 0 auto;
            padding: 2rem;
        }

        nav {
            margin-bottom: 3rem;
        }

        nav a {
            color: var(--accent);
            text-decoration: none;
            font-weight: 500;
            padding: 0.5rem 0;
            border-bottom: 2px solid transparent;
            transition: border-color 0.2s;
        }

        nav a:hover {
            border-bottom-color: var(--accent);
        }

        h1 {
            font-size: 2.5rem;
            font-weight: 700;
            margin-bottom: 1.5rem;
            color: var(--primary);
            line-height: 1.3;
        }

        h2 {
            font-size: 1.8rem;
            font-weight: 600;
            margin: 2rem 0 1rem;
            color: var(--primary);
        }

        h3 {
            font-size: 1.4rem;
            font-weight: 600;
            margin: 1.5rem 0 1rem;
            color: var(--primary);
        }

        p {
            margin-bottom: 1.5rem;
            color: var(--secondary);
        }

        ul, ol {
            margin: 1.5rem 0;
            padding-left: 1.5rem;
            color: var(--secondary);
        }

        li {
            margin-bottom: 0.5rem;
        }

        .recipe-meta {
            background: var(--surface);
            padding: 1.5rem;
            border-radius: 8px;
            margin-bottom: 3rem;
            box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
        }

        .tag {
            display: inline-block;
            background: var(--background);
            color: var(--muted);
            padding: 0.2rem 0.4rem;
            border-radius: 4px;
            font-size: 0.75rem;
            font-weight: 500;
            border: 1px solid var(--border);
            transition: all 0.2s;
        }

        .tag:hover {
            color: var(--accent);
            border-color: var(--accent);
        }

        a {
            color: var(--accent);
            text-decoration: none;
            transition: color 0.2s;
        }

        a:hover {
            color: var(--primary);
        }

        /* Code blocks */
        pre {
            background: var(--surface);
            padding: 1.5rem;
            border-radius: 8px;
            overflow-x: auto;
            margin: 1.5rem 0;
            border: 1px solid var(--border);
        }

        code {
            font-family: 'SF Mono', Menlo, Monaco, Consolas, monospace;
            font-size: 0.9rem;
            color: var(--secondary);
        }

        /* Tables */
        table {
            width: 100%;
            border-collapse: collapse;
            margin: 1.5rem 0;
        }

        th, td {
            padding: 0.75rem;
            text-align: left;
            border-bottom: 1px solid var(--border);
        }

        th {
            font-weight: 600;
            color: var(--primary);
        }

        /* Blockquotes */
        blockquote {
            border-left: 4px solid var(--accent);
            padding-left: 1.5rem;
            margin: 1.5rem 0;
            color: var(--muted);
            font-style: italic;
        }

        /* Images */
        img {
            max-width: 100%;
            height: auto;
            border-radius: 8px;
            margin: 1.5rem 0;
        }

        @media (max-width: 768px) {
            body {
                padding: 1.5rem;
            }

            h1 {
                font-size: 2rem;
            }

            h2 {
                font-size: 1.5rem;
            }

            h3 {
                font-size: 1.2rem;
            }
        }
    </style>
</head>
<body>
    <nav><a href="index.html">← All Recipes</a></nav>
    <h1>{{.Title}}</h1>
    <div class="recipe-meta">
        {{range .Tags}}<span class="tag">{{.}}</span>{{end}}
    </div>
    {{.HTMLContent}}
</body>
</html>`))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, recipe); err != nil {
		return err
	}

	return ioutil.WriteFile(
		filepath.Join(sg.OutputDir, recipe.Slug+".html"),
		buf.Bytes(),
		0644,
	)
}

func (sg *SiteGenerator) generateIndexPage() error {
	tmpl := template.Must(template.New("index").Parse(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Gabe's Recipes</title>
    <style>
        :root {
            color-scheme: dark light;
            
            --primary: #E2E8F0;
            --secondary: #CBD5E0;
            --accent: #B794F4;
            --background: #1A202C;
            --surface: #2D3748;
            --text: #F7FAFC;
            --muted: #A0AEC0;
            --border: #4A5568;
        }

        @media (prefers-color-scheme: light) {
            :root {
                --primary: #2D3748;
                --secondary: #4A5568;
                --accent: #553C9A;
                --background: #ffffff;
                --surface: #F7FAFC;
                --text: #1A202C;
                --muted: #718096;
                --border: #E2E8F0;
            }
        }

        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            line-height: 1.7;
            color: var(--text);
            background: var(--background);
            max-width: 800px;
            margin: 0 auto;
            padding: 2rem;
        }

        h1 {
            font-size: 2.5rem;
            font-weight: 700;
            margin-bottom: 2.5rem;
            color: var(--primary);
            text-align: center;
        }

        .recipe-list {
            list-style: none;
            padding: 0;
            display: grid;
            grid-template-columns: 1fr;
            gap: 0.5rem;
            margin: 0 auto;
            max-width: 1200px;
            padding: 0 1rem;
        }

        @media (min-width: 768px) {
            .recipe-list {
                grid-template-columns: repeat(2, 1fr);
                gap: 1rem;
            }
        }

        .recipe-item {
            background: var(--surface);
            padding: 0.75rem 1rem;
            border-radius: 6px;
            transition: background-color 0.2s;
            display: flex;
            align-items: center;
            justify-content: space-between;
        }

        .recipe-item:hover {
            background: var(--border);
        }

        @media (max-width: 480px) {
            .recipe-item {
                flex-direction: column;
                align-items: flex-start;
                gap: 0.5rem;
            }
            
            .tags {
                width: 100%;
                justify-content: flex-start;
            }
        }

        .recipe-item a {
            color: var(--primary);
            text-decoration: none;
            font-weight: 500;
            transition: color 0.2s;
        }

        .recipe-item .tags {
            display: flex;
            gap: 0.25rem;
            flex-wrap: wrap;
        }

        .recipe-item a:hover {
            color: var(--accent);
        }

        .tag {
            display: inline-block;
            background: var(--background);
            color: var(--accent);
            padding: 0.4rem 0.8rem;
            border-radius: 6px;
            margin-right: 0.5rem;
            margin-bottom: 0.5rem;
            font-size: 0.9rem;
            font-weight: 500;
            border: 1px solid var(--border);
            transition: all 0.2s;
        }

        .tag:hover {
            background: var(--accent);
            color: white;
            border-color: var(--accent);
        }

        @media (max-width: 768px) {
            body {
                padding: 1.5rem;
            }

            h1 {
                font-size: 2rem;
                margin-bottom: 2rem;
            }

            .recipe-list {
                grid-template-columns: 1fr;
            }
        }
    </style>
</head>
<body>
    <h1>Gabe's Recipes</h1>
    <ul class="recipe-list">
        {{range .Recipes}}
        <li class="recipe-item">
            <a href="{{.Slug}}.html">{{.Title}}</a>
            <div class="tags">
                {{range .Tags}}<span class="tag">{{.}}</span>{{end}}
            </div>
        </li>
        {{end}}
    </ul>
</body>
</html>`))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sg); err != nil {
		return err
	}

	return ioutil.WriteFile(
		filepath.Join(sg.OutputDir, "index.html"),
		buf.Bytes(),
		0644,
	)
}

func (sg *SiteGenerator) uploadToS3() error {
	const concurrency = 5
	files := make(chan string)
	var wg sync.WaitGroup

	// Start worker pool
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range files {
				if err := sg.uploadFile(context.TODO(), file); err != nil {
					fmt.Fprintf(os.Stderr, "Error uploading %s: %v\n", file, err)
				}
			}
		}()
	}

	// Find files to upload
	err := filepath.Walk(sg.OutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files <- path
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory: %w", err)
	}

	close(files)
	wg.Wait()
	return nil
}

func (sg *SiteGenerator) uploadFile(ctx context.Context, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	key := strings.TrimPrefix(filename, sg.OutputDir+"/")

	cacheControl := "public, max-age=31536000"
	if filepath.Ext(filename) == ".html" {
		cacheControl = "no-cache, no-store, must-revalidate"
	}

	_, err = sg.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       &sg.s3Bucket,
		Key:          &key,
		Body:         file,
		ContentType:  &contentType,
		CacheControl: &cacheControl,
	})
	if err != nil {
		return fmt.Errorf("uploading to S3: %w", err)
	}

	fmt.Printf("Uploaded %s\n", key)
	return nil
}

func main() {
	recipesDir := flag.String("recipes", "recipes", "Directory containing recipe markdown files")
	outputDir := flag.String("output", "dist", "Output directory for generated site")
	s3Bucket := flag.String("bucket", "", "S3 bucket name (optional)")
	flag.Parse()

	var s3Client *s3.Client
	if *s3Bucket != "" {
		cfg, err := config.LoadDefaultConfig(context.TODO())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to load SDK config: %v\n", err)
			os.Exit(1)
		}
		s3Client = s3.NewFromConfig(cfg)
	}

	generator := NewSiteGenerator(*recipesDir, *outputDir, s3Client, *s3Bucket)
	if err := generator.Generate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
