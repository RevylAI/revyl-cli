#! /usr/bin/env node
import chalk from 'chalk';
import boxen from 'boxen';
import yargs from 'yargs';
import { hideBin } from 'yargs/helpers';
import fs from 'fs';
import path from 'path';
import dotenv from 'dotenv';
import yaml from 'js-yaml';
import axios from 'axios';
import ora from 'ora';


// Load environment variables
dotenv.config();

  
const usage = chalk.blue("\nUsage: revyl <command> [options]\n"
  + boxen(chalk.green("\n" + "Revyl CLI Tool" + "\n"), {padding: 1, borderColor: 'green', dimBorder: true}) + "\n");


async function runTest(testId, speed) {
  const REVYL_API_KEY = process.env.REVYL_API_KEY;
  const url = process.env.REVYL_API_URL || 'https://device.cognisim.io/execute_test_id';

  const spinner = ora(`Running test ${testId}... \n`).start();

  try {
    const response = await axios.post(url, { 
      test_id: testId,
      speed: speed // Send the speed value directly
    }, {
      headers: {
        'Authorization': `Bearer ${REVYL_API_KEY}`,
        'Content-Type': 'application/json'
      }
    });

    if (response.data && response.data.success) {
      spinner.succeed(chalk.green(`Test ${testId} ran successfully`));
      return { testId, success: true, result: response.data };
    } else {
      spinner.fail(chalk.red(`Test ${testId} failed`));
      return { testId, success: false, result: response.data };
    }
  } catch (error) {
    spinner.fail(chalk.red(`Error running test ${testId}:`, error.message));
    return { testId, success: false, error: error.message };
  }
}

async function executeWorkflow(workflowData) {
  const speed = workflowData.speed || 2; // Default to speed 2 if not specified
  const parallel = workflowData.parallel || false;

  console.log(chalk.cyan(`Executing workflow: ${workflowData.name} (v${workflowData.version})`));
  console.log(chalk.cyan(`Description: ${workflowData.description}`));
  console.log(chalk.cyan(`Speed: ${speed}`));
  console.log(chalk.cyan(`Parallel: ${parallel}`));

  console.log(chalk.yellow('Running tests:'));

  try {
    const testResults = [];
    //const spinners = workflowData.test_ids.map(testId => ora(`Running test ${testId}...`).start());

    if (parallel) {
      const results = await Promise.all(workflowData.test_ids.map((testId, index) => {
        return runTest(testId, speed).then(result => {
          if (result.success) {
            spinners[index].succeed(chalk.green(`Test ${testId} ran successfully`));
          } else {
            spinners[index].fail(chalk.red(`Test ${testId} failed`));
            if (result.error) {
              spinners[index].fail(chalk.red(`Error: ${result.error}`));
            }
          }
          return result;
        }).catch(error => {
          spinners[index].fail(chalk.red(`Error running test ${testId}: ${error.message}`));
          return { testId, success: false, error: error.message };
        });
      }));
      testResults.push(...results);
    } else {
      for (const [index, testId] of workflowData.test_ids.entries()) {
        const result = await runTest(testId, speed);
        if (result.success) {
          spinners[index].succeed(chalk.green(`Test ${testId} ran successfully`));
        } else {
          spinners[index].fail(chalk.red(`Test ${testId} failed`));
          if (result.error) {
            spinners[index].fail(chalk.red(`Error: ${result.error}`));
          }
        }
        testResults.push(result);
      }
    }

    console.log(chalk.yellow('\nTest Results Summary:'));
    testResults.forEach(result => {
      if (result.success) {
        console.log(chalk.green(`Test ${result.testId}: Passed`));
      } else {
        console.log(chalk.red(`Test ${result.testId}: Failed`));
        if (result.error) {
          console.log(chalk.red(`  Error: ${result.error}`));
        }
      }
    });

    const allPassed = testResults.every(result => result.success);
    if (allPassed) {
      console.log(chalk.green('\nAll tests passed successfully!'));
    } else {
      console.log(chalk.red('\nSome tests failed. Please check the results above.'));
    }
  } catch (error) {
    console.error(chalk.red('Error executing workflow:', error.message));
  }
}

yargs(hideBin(process.argv))
  .usage(usage)
  .command('run [file]', 'Run a YAML file from the .revyl folder', (yargs) => {
    return yargs
      .positional('file', {
        describe: 'YAML file to run',
        type: 'string'
      });
  }, async (argv) => {
    const REVYL_API_KEY = process.env.REVYL_API_KEY;

    if (!REVYL_API_KEY) {
      console.error(chalk.red('Error: REVYL_API_KEY not found in environment variables.'));
      process.exit(1);
    }

    const revylFolder = path.join(process.cwd(), '.revyl');
    
    if (!fs.existsSync(revylFolder)) {
      console.error(chalk.red('Error: .revyl folder not found in the current directory.'));
      process.exit(1);
    }

    const fileName = argv.file || 'index.yml';
    const filePath = path.join(revylFolder, fileName);

    if (!fs.existsSync(filePath)) {
      console.error(chalk.red(`Error: File ${filePath} not found.`));
      process.exit(1);
    }

    if (!filePath.endsWith('.yml') && !filePath.endsWith('.yaml')) {
      console.error(chalk.red('Error: File must be a YAML file (.yml or .yaml)'));
      process.exit(1);
    }

    console.log(chalk.green(`Running YAML file: ${filePath}`));
    try {
      const fileContents = fs.readFileSync(filePath, 'utf8');
      const data = yaml.load(fileContents);
      await executeWorkflow(data);
    } catch (err) {
      console.error(chalk.red('Error reading, parsing, or executing YAML file:'), err);
    }
  })
  .command('help', 'Show help', () => {}, (argv) => {
    console.log(chalk.yellow(boxen(
      chalk.bold('Revyl CLI Help\n\n') +
      chalk.green('Commands:\n') +
      '  run [file]  Run a YAML file from the .revyl folder\n' +
      '  help        Show this help message\n\n' +
      chalk.green('Options:\n') +
      '  --version   Show version number\n' +
      '  --help      Show help\n\n' +
      chalk.green('Examples:\n') +
      '  revyl run\n' +
      '  revyl run myfile.yml\n' +
      '  revyl help',
      {padding: 1, borderColor: 'yellow', dimBorder: true}
    )));
  })
  .demandCommand(1, 'You need to specify a command.')
  .help()
  .alias('h', 'help')
  .version('v', 'Show version number', '1.0.0')
  .alias('v', 'version')
  .argv;