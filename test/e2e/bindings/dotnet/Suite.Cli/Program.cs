// -----------------------------------------------------------------------
// <copyright file="Program.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Bdd.Suite.Run;
using Cordis.E2E;

// suiterun for the cordis behaviour suites: lint, list and run them against the .NET port.
return Runner.Run(args, Console.Out, Console.Error, new CordisExtension());
