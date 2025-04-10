package Goh

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// executeCommand splits the input command string, executes it, and prints any error to stderr.
func executeCommand(command string) {
	parts := strings.Split(command, " ")
	if len(parts) == 0 {
		return
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		fmt.Println(err)
	}
}

// CodeGenerator is a struct for generating Go code from parsed template blocks.
type CodeGenerator struct {
	DefinedFunction *Block
	RootBlocks      Blocks
	OutputFile      *os.File
	Buffer          *bytes.Buffer
	ConstantLength  int
	BufferName      string
	RawCode         string
	PackageName     string
	Destination     string
}

// NewGenerator initializes a new code generator, parses the given template file, and sets up the necessary fields for code generation.
func (g *CodeGenerator) NewGenerator(templateFilePath string) {
	if g.PackageName == "" {
		g.PackageName = "template"
	}
	g.Buffer = bytes.NewBuffer(nil)

	// Parse the template file using Parser
	blocks, rawCode, definedFunc := (&Parser{}).Parse(templateFilePath)

	// Directly assign the parsed data to the expected types
	g.RootBlocks = blocks
	g.RawCode = rawCode
	g.DefinedFunction = definedFunc

	outputFile, err := os.Create(path.Join(g.Destination, path.Base(templateFilePath)+".go"))
	if err != nil {
		panic(err.Error())
	}
	outputFile.WriteString("// DO NOT EDIT!\n// Generated by Goh\n\n")
	g.OutputFile = outputFile
	g.GenerateCode()
	fmt.Println("\033[0;32mSuccess\033[0m", templateFilePath)
}

// GenerateCode writes the generated Go code to the output file based on the parsed template and defined function.
func (g *CodeGenerator) GenerateCode() {
	g.OutputFile.WriteString("package ")
	g.OutputFile.WriteString(g.PackageName)
	g.OutputFile.WriteString("\nimport (\n\t\"bytes\"\n\t\"github.com/OblivionOcean/Goh/utils\"\n)\n\n")

	if g.DefinedFunction == nil {
		return
	}

	code, bufferName, err := g.generateFunction(g.DefinedFunction)
	if err != nil {
		panic(err.Error())
	}
	g.BufferName = bufferName
	g.OutputFile.WriteString(g.RawCode)
	g.OutputFile.WriteString(code)
	g.OutputFile.WriteString(fmt.Sprintf("{\n%s.Grow(", bufferName))

	for i := 0; i < len(g.RootBlocks); i++ {
		block := g.RootBlocks[i]
		switch block.BlockType {
		case TypeCode:
			g.Buffer.WriteString(block.Content)
			g.Buffer.WriteString("\n")
		case TypeHTML:
			g.generateEscapedHTML(block)
		case TypeEscape:
			g.generateEscapedHTML(block)
		case TypeValue:
			g.generateValue(block)
		case TypeExtend:
			continue
		}
	}

	g.OutputFile.WriteString(strconv.Itoa(g.ConstantLength))
	g.OutputFile.WriteString(")\n")
	g.OutputFile.ReadFrom(g.Buffer)
	g.OutputFile.WriteString("}\n")
}

// generateFunction parses a block to extract and validate a function, returning the function's code, buffer name, and any error.
func (g *CodeGenerator) generateFunction(block *Block) (code string, bufferName string, err error) {
	source := []byte("package Goh\n")
	source = append(source, block.Content...)

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "", source, parser.AllErrors)
	if err != nil {
		return
	}

	functionDecl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		return "", "", errors.New("definition is not a function type")
	}

	parameters := functionDecl.Type.Params.List
	if len(parameters) == 0 {
		err = errors.New("function parameters should not be empty")
		return
	}

	lastParameter := parameters[len(parameters)-1]
	expression := lastParameter.Type
	if starExpr, ok := expression.(*ast.StarExpr); ok {
		expression = starExpr.X
	}
	selectorExpr, ok := expression.(*ast.SelectorExpr)
	if !ok {
		err = errors.New("function parameters should not be empty")
		return
	}

	if selectorExpr.X.(*ast.Ident).Name != "bytes" || selectorExpr.Sel.Name != "Buffer" {
		err = errors.New("function parameters should not be empty")
		return
	}

	if n := len(lastParameter.Names); n > 0 {
		bufferName = lastParameter.Names[n-1].Name
	}
	code = block.Content
	return
}

// generateValue processes a block to generate the appropriate Go code for writing the block's content to the buffer.
func (g *CodeGenerator) generateValue(block *Block) {
	// Trim the block content
	block.Content = strings.TrimSpace(block.Content)
	if len(block.Content) == 0 {
		return
	}

	// Define a map of variable type handlers
	varTypeHandlers := map[int]func(string, string) string{
		VarTypeString: func(content, bufferName string) string {
			// Generate code for string type
			return fmt.Sprintf("%s.WriteString(%s)\n", bufferName, content)
		},
		VarTypeBytes: func(content, bufferName string) string {
			// Generate code for bytes type
			return fmt.Sprintf("%s.Write(%s)\n", bufferName, content)
		},
		VarTypeInt: func(content, bufferName string) string {
			// Generate code for int type
			return fmt.Sprintf("Goh.FormatInt(int64(%s), %s)\n", content, bufferName)
		},
		VarTypeUint: func(content, bufferName string) string {
			// Generate code for uint type
			return fmt.Sprintf("Goh.FormatUint(uint64(%s), %s)\n", content, bufferName)
		},
		VarTypeBool: func(content, bufferName string) string {
			// Generate code for bool type and update constant length
			g.ConstantLength += 5
			return fmt.Sprintf("Goh.FormatBool(%s, %s)\n", content, bufferName)
		},
		VarTypeAny: func(content, bufferName string) string {
			// Generate code for any type
			return fmt.Sprintf("Goh.FormatAny(%s, %s)\n", content, bufferName)
		},
	}

	// Get the handler function from the map and generate the code
	if handler, exists := varTypeHandlers[block.VariableType]; exists {
		code := handler(block.Content, g.BufferName)
		g.Buffer.WriteString(code)
	}
}

// generateEscapedHTML processes a block and generates Go code to escape HTML content based on the block's variable type.
func (g *CodeGenerator) generateEscapedHTML(block *Block) {
	// Trim the block content
	block.Content = strings.TrimSpace(block.Content)
	if len(block.Content) == 0 {
		return
	}

	// Define a map of variable type handlers
	varTypeHandlers := map[int]func(string, string) string{
		VarTypeString: func(content, bufferName string) string {
			// Generate code for string type
			return fmt.Sprintf("Goh.EscapeHTML(%s, %s)\n", content, bufferName)
		},
		VarTypeBytes: func(content, bufferName string) string {
			// Generate code for bytes type
			return fmt.Sprintf("Goh.EscapeHTML(Goh.Bytes2String(%s), %s)\n", content, bufferName)
		},
		VarTypeInt: func(content, bufferName string) string {
			// Generate code for int type
			return fmt.Sprintf("Goh.FormatInt(int64(%s), %s)\n", content, bufferName)
		},
		VarTypeUint: func(content, bufferName string) string {
			// Generate code for uint type
			return fmt.Sprintf("Goh.FormatUint(uint64(%s), %s)\n", content, bufferName)
		},
		VarTypeBool: func(content, bufferName string) string {
			// Generate code for bool type and update constant length
			g.ConstantLength += 5
			return fmt.Sprintf("Goh.FormatBool(%s, %s)\n", content, bufferName)
		},
		VarTypeAny: func(content, bufferName string) string {
			// Generate code for any type
			return fmt.Sprintf("Goh.FormatAny(%s, %s)\n", content, bufferName)
		},
	}

	// Get the handler function from the map and generate the code
	if handler, exists := varTypeHandlers[block.VariableType]; exists {
		generatedCode := handler(block.Content, g.BufferName)
		g.Buffer.WriteString(generatedCode)
	} else {
		// Handle unsupported variable type
		panic(fmt.Sprintf("Unsupported value type: %d", block.VariableType))
	}
}
