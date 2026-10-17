package console

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"bfiber/internal/console/core"
)

// MakeSubOptions 封装独立生成子命令（make:schema, make:factory, make:seeder）的入参。
type MakeSubOptions struct {
	Name    string
	Chinese string
	Force   bool
	DryRun  bool
	Factory Factory
}

// Complete 提取命令行参数或交互式提示。
func (o *MakeSubOptions) Complete(promptTitle, promptDesc string, args []string) error {
	if len(args) > 0 {
		o.Name = strings.TrimSpace(args[0])
		return nil
	}

	streams := o.Factory.IOStreams()
	if streams.CanPrompt() {
		return huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(promptTitle).
					Description(promptDesc).
					Value(&o.Name).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return errors.New("name cannot be empty")
						}
						return nil
					}),
				huh.NewInput().
					Title("Chinese Name (Optional)").
					Description("Business entity description (e.g. 商品, 订单)").
					Value(&o.Chinese),
			),
		).Run()
	}

	return nil
}

// Validate 校验参数合法性。
func (o *MakeSubOptions) Validate(cmdName string) error {
	if o.Name == "" {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("accepts 1 arg(s), received 0. Usage: bfiber %s <name>", cmdName)}
	}
	return nil
}

// toGeneratorOptions 转换为核心生成器选项。
func (o *MakeSubOptions) toGeneratorOptions() core.GeneratorOptions {
	return core.GeneratorOptions{
		Name:        o.Name,
		ChineseName: o.Chinese,
		Force:       o.Force,
		DryRun:      o.DryRun,
	}
}

// newMakeSchemaCmd 构建单独生成 Atlas Schema SQL DDL 模式的命令。
func newMakeSchemaCmd(f Factory) *cobra.Command {
	opts := &MakeSubOptions{Factory: f}

	cmd := &cobra.Command{
		Use:   "make:schema [name]",
		Short: "Generate an Atlas PostgreSQL 18 schema DDL file",
		Long: `Generate a declarative PostgreSQL 18 DDL schema file under internal/database/schemas/.

Examples:
  bfiber make:schema product
  bfiber make:schema order --chinese "订单"
  bfiber make:schema article --force
  bfiber make:schema customer --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete("Table / Schema Name", "Singular or plural identifier (e.g. product, order)", args); err != nil {
				return err
			}
			if err := opts.Validate("make:schema"); err != nil {
				return err
			}

			streams := f.IOStreams()
			genOpts := opts.toGeneratorOptions()
			core.TitleTo(streams.Out, fmt.Sprintf("Generating Schema DDL: %s (DryRun=%v)", opts.Name, opts.DryRun))

			results, err := core.GenerateSchema(genOpts)
			if err != nil {
				return err
			}

			counts := printActionResults(streams.Out, results)
			if opts.DryRun {
				core.SuccessTo(streams.Out, "Dry run completed: %d files planned.", counts.dryRuns)
				return nil
			}

			core.SuccessTo(streams.Out, "Schema generation finished: %d created, %d overwritten, %d skipped.", counts.created, counts.overwritten, counts.skipped)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, core.FlagForce, "f", false, "Force overwrite existing schema file")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Simulate operation without writing files")
	cmd.Flags().StringVar(&opts.Chinese, "chinese", "", "Custom Chinese business description")

	return cmd
}

// newMakeFactoryCmd 构建单独生成数据模型 Factory 的命令。
func newMakeFactoryCmd(f Factory) *cobra.Command {
	opts := &MakeSubOptions{Factory: f}

	cmd := &cobra.Command{
		Use:   "make:factory [name]",
		Short: "Generate a database model factory file",
		Long: `Generate a domain model factory file under internal/database/factories/ with adaptive Faker rules.

Examples:
  bfiber make:factory product
  bfiber make:factory order --chinese "订单"
  bfiber make:factory article --force
  bfiber make:factory user --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete("Model Name", "Singular identifier (e.g. product, order)", args); err != nil {
				return err
			}
			if err := opts.Validate("make:factory"); err != nil {
				return err
			}

			streams := f.IOStreams()
			genOpts := opts.toGeneratorOptions()
			core.TitleTo(streams.Out, fmt.Sprintf("Generating Model Factory: %s (DryRun=%v)", opts.Name, opts.DryRun))

			results, err := core.GenerateFactory(genOpts)
			if err != nil {
				return err
			}

			counts := printActionResults(streams.Out, results)
			if opts.DryRun {
				core.SuccessTo(streams.Out, "Dry run completed: %d files planned.", counts.dryRuns)
				return nil
			}

			core.SuccessTo(streams.Out, "Factory generation finished: %d created, %d overwritten, %d skipped.", counts.created, counts.overwritten, counts.skipped)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, core.FlagForce, "f", false, "Force overwrite existing factory file")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Simulate operation without writing files")
	cmd.Flags().StringVar(&opts.Chinese, "chinese", "", "Custom Chinese business description")

	return cmd
}

// newMakeSeederCmd 构建单独生成数据播种器 Seeder 的命令。
func newMakeSeederCmd(f Factory) *cobra.Command {
	opts := &MakeSubOptions{Factory: f}

	cmd := &cobra.Command{
		Use:   "make:seeder [name]",
		Short: "Generate a database seeder file",
		Long: `Generate a test data seeder file under internal/database/seeders/ with progress hook support.

Examples:
  bfiber make:seeder product
  bfiber make:seeder order --chinese "订单"
  bfiber make:seeder article --force
  bfiber make:seeder customer --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete("Seeder / Entity Name", "Singular identifier (e.g. product, order)", args); err != nil {
				return err
			}
			if err := opts.Validate("make:seeder"); err != nil {
				return err
			}

			streams := f.IOStreams()
			genOpts := opts.toGeneratorOptions()
			core.TitleTo(streams.Out, fmt.Sprintf("Generating Database Seeder: %s (DryRun=%v)", opts.Name, opts.DryRun))

			results, err := core.GenerateSeeder(genOpts)
			if err != nil {
				return err
			}

			counts := printActionResults(streams.Out, results)
			if opts.DryRun {
				core.SuccessTo(streams.Out, "Dry run completed: %d files planned.", counts.dryRuns)
				return nil
			}

			core.SuccessTo(streams.Out, "Seeder generation finished: %d created, %d overwritten, %d skipped.", counts.created, counts.overwritten, counts.skipped)

			// 自动尝试挂载至 database_seeder.go
			if injected, err := core.InjectSeeder(genOpts.RootDir, genOpts); err != nil {
				core.WarnTo(streams.Out, "Failed to automatically register seeder: %v", err)
			} else if injected {
				core.SuccessTo(streams.Out, "  * Seeder registered in internal/database/seeders/database_seeder.go")
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, core.FlagForce, "f", false, "Force overwrite existing seeder file")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Simulate operation without writing files")
	cmd.Flags().StringVar(&opts.Chinese, "chinese", "", "Custom Chinese business description")

	return cmd
}
